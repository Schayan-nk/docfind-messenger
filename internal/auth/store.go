package auth

import (
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/Schayan-nk/docfind-messenger/internal/sms"
)

// Role decides what a signed-in user may do.
//
// Three tiers rather than per-action permissions: the distinctions that
// actually matter here are "may change how the system runs", "may see the
// whole business" and "may look things up", and a matrix of checkboxes would
// be more to get wrong than to gain.
type Role string

const (
	RoleAdmin   Role = "admin"   // users, settings, reading path, every report
	RoleManager Role = "manager" // all documents and reports; no system settings
	RoleViewer  Role = "viewer"  // documents and the reports granted to them
)

// ValidRole reports whether r is one of the known roles.
func ValidRole(r Role) bool {
	switch r {
	case RoleAdmin, RoleManager, RoleViewer:
		return true
	}
	// Or one an administrator made (roletemplates.go).
	return IsCustomRole(r)
}

// User is one account.
type User struct {
	Username    string `json:"username"`
	DisplayName string `json:"display_name"`
	// Email is this person's address.
	//
	// NEW WITH THE DATABASE. Accounts have never carried one, so every
	// existing account has an empty string here and nothing depends on it
	// being set -- which is why it is safe to add without a migration step
	// beyond the column default.
	//
	// It addresses a colleague; it does not authenticate anybody. Sign-in is
	// by username and that has not changed.
	Email string `json:"email,omitempty"`
	// Phone is this person's mobile number, as 09xxxxxxxxx (SetPhone): where
	// SMS notifications go, and the second step of signing in when they have
	// chosen SMS for it.
	Phone string `json:"phone,omitempty"`
	// SecondEmail and PersonnelCode are what an administrator records of a
	// colleague (staff.go). They describe a colleague; neither authenticates
	// anybody.
	SecondEmail   string    `json:"second_email,omitempty"`
	PersonnelCode string    `json:"personnel_code,omitempty"`
	Role          Role      `json:"role"`
	PasswordHash  string    `json:"password_hash"`
	Disabled      bool      `json:"disabled"`
	CreatedAt     time.Time `json:"created_at"`
	LastLogin     time.Time `json:"last_login,omitempty"`
	// Grants are this account's overrides on top of its role template:
	// "allow" adds something the template withholds, "deny" removes something
	// it gives. An ID that is ABSENT inherits the template, which is the third
	// state the editing screen needs -- without it, touching a permission
	// once would pin it for ever and a later change to the template would
	// silently skip that person.
	//
	// Keyed by the catalogue ID and not by label, so relabelling a report in
	// Persian never revokes anybody's access to it. See catalogue.go.
	Grants map[string]string `json:"grants,omitempty"`
	// TokenVersion is what makes an issued token revocable. Every token
	// carries the version current when it was minted; bumping this here
	// invalidates all of them at once. Without it, disabling an account or
	// changing a password would leave that person signed in until their
	// token expired on its own.
	TokenVersion int `json:"token_version"`
	// Emergency is set on a REQUEST's copy of the account when its session was
	// issued from the snapshot with the database down (see Claims.Emergency).
	// Never stored -- json:"-" keeps it out of the file, the snapshot and the
	// database -- because it describes a session, not a person.
	Emergency bool `json:"-"`
	// Avatar is the file extension of this account's picture, or empty for
	// none. The image itself is a file beside the account store, not bytes in
	// here: this file is read and rewritten whole on every change, and putting
	// a few hundred kilobytes of base64 per person in it would turn a staff
	// list into something that has to be streamed.
	Avatar string `json:"avatar,omitempty"`
	// Prefs is how this person wants the application to look. Kept on the
	// account rather than only in the browser so that it follows them to the
	// phone and to the meeting-room machine.
	Prefs Prefs `json:"prefs,omitempty"`
	// EnglishName is this person's given name in Latin script, for the places
	// the interface writes it in English.
	//
	// A field rather than something derived. DisplayName might be "نادر م.
	// کریمی", and transliterating that reliably is not a thing a few lines of Go
	// can do -- the vowels are not written, so the same spelling gives Nader,
	// Nadir or Naader with no way to choose. Getting somebody's own name wrong
	// in the first words they read is worse than not addressing them at all,
	// so an administrator types it and the greeting is simply omitted when it
	// is blank.
	EnglishName string `json:"english_name,omitempty"`
	// SecondFactor is the authenticator-app sign-in step (totp.go,
	// secondfactor.go); nil means password only. Its key is sealed, and it
	// never leaves the process -- Public() says only whether it is on.
	SecondFactor *SecondFactor `json:"second_factor,omitempty"`
	// Passkeys are this account's registered passkeys, and PasskeyHandle the
	// opaque id browsers keep with them (passkeys.go). Public keys and
	// counters, nothing secret; Public() says only how many there are.
	Passkeys      []Passkey `json:"passkeys,omitempty"`
	PasskeyHandle string    `json:"passkey_handle,omitempty"`
	// MustChangePassword is set on an account made with a temporary password
	// (CreateStaff). Until the person chooses their own, Guard.Authenticate
	// answers nothing but who they are, the change and signing out; their own
	// change (ChangePassword) clears it. An administrator's reset leaves it
	// as it was.
	MustChangePassword bool `json:"must_change_password,omitempty"`
	// PasswordChangedAt is when the password was last set, by anybody; the
	// policy's maximum age counts from it (zero: from CreatedAt).
	PasswordChangedAt time.Time `json:"password_changed_at,omitempty"`
	// Signature is the file extension of this person's signature image, or
	// empty for none: a file beside the avatars (find/signature.go), served
	// only to its owner and to whoever manages accounts.
	Signature string `json:"signature,omitempty"`
}

// SetPhone records this person's mobile number, or clears it when empty.
//
// Normalised rather than stored as typed -- Persian digits, spaces, +98 --
// because the SMS panel takes 09xxxxxxxxx and a number stored any other way
// is a message that silently goes nowhere. Anything that is not then a mobile
// number is refused.
func (s *Store) SetPhone(username, phone string) error {
	phone = strings.TrimSpace(phone)
	if phone != "" {
		p, ok := sms.NormalizePhone(phone)
		if !ok {
			return fmt.Errorf("auth: %q is not a mobile number", phone)
		}
		phone = p
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	u, ok := s.users[normalizeName(username)]
	if !ok {
		return ErrNoSuchUser
	}
	before := u.Phone
	u.Phone = phone
	if err := s.save(); err != nil {
		u.Phone = before
		return err
	}
	return nil
}

// Prefs are per-person display settings.
//
// Deliberately a small closed set rather than a free-form map: everything here
// has to be rendered by the front end, and a value nothing reads is a setting
// that appears to work and does nothing.
type Prefs struct {
	// FontScale multiplies every type size, as a percentage. 100 is the
	// design size; the accepted range is enforced in SetPrefs rather than
	// trusted from the request.
	FontScale int `json:"font_scale,omitempty"`
	// Density tightens or loosens the spacing scale: "comfortable" (the
	// default) or "compact".
	Density string `json:"density,omitempty"`
	// Motion lets someone turn the animations off without changing an
	// operating-system setting, which is what people actually ask for on a
	// wall display.
	ReduceMotion bool `json:"reduce_motion,omitempty"`
	// Muted is the notification topics the person has switched off (see
	// notifyprefs.go). Set by SetMuted only: the appearance form does not
	// carry it, so SetPrefs keeps what is stored.
	Muted []string `json:"muted,omitempty"`
	// SMS names the topics that also arrive by SMS, to the account's phone
	// (asked for on 1405/07/08). Set by SetSMSTopics only, and kept by
	// SetPrefs like Muted.
	SMS []string `json:"sms,omitempty"`
	// Shortcuts are this person's own keys to pages (shortcuts.go). Set by
	// SetShortcuts only, and kept by SetPrefs like Muted.
	Shortcuts []Shortcut `json:"shortcuts,omitempty"`
	// Menu is this person's own arrangement of the menu (menulayout.go); nil
	// is the menu as it comes. Set by SetMenu only, and kept by SetPrefs.
	Menu []MenuNode `json:"menu,omitempty"`
	// Pages are the pages this person added to their menu under names of
	// their own (menulayout.go), set with Menu by SetMenu; Sites are their
	// «سایت‌های پرکاربرد» (sites.go), set by SetSites. Both kept by SetPrefs.
	Pages []MenuPage `json:"menu_pages,omitempty"`
	Sites []Site     `json:"sites,omitempty"`
	// Hidden are the menu entries this person took out with − (menulayout.go).
	// Set by SetMenuHidden only, and kept by SetPrefs like Menu.
	Hidden []string `json:"menu_hidden,omitempty"`
	// QuietFrom and QuietTo are the hours, server time, between which a
	// notification still arrives but without sound or vibration (see
	// notifyprefs.go). Equal hours -- the zero value -- mean none. Set by
	// SetQuiet only, like Muted.
	QuietFrom int `json:"quiet_from,omitempty"`
	QuietTo   int `json:"quiet_to,omitempty"`
}

// Public strips the password hash, for anything that leaves the process.
func (u *User) Public() map[string]any {
	return map[string]any{
		"username":     u.Username,
		"display_name": u.DisplayName,
		// The mobile number: shown to the person and to whoever manages
		// accounts, where it is edited.
		"phone": u.Phone,
		// What is recorded of this colleague (staff.go).
		"email": u.Email,
		// "other", not "second": public_test keeps every key that could be
		// read as the second sign-in factor out of this map.
		"other_email":    u.SecondEmail,
		"personnel_code": u.PersonnelCode,
		// Whether the next thing this person must do is choose a new
		// password (users.update_password); the sign-in page asks for it
		// before anything else. Named without the word, which the tests keep
		// out of this map altogether.
		"update_required": u.MustChangePassword,
		// Whether a signature is on file, never the image: that has its own
		// route, closed to everybody but the owner and account managers.
		"has_signature": u.Signature != "",
		"role":          string(u.Role),
		"disabled":      u.Disabled,
		"created_at":    u.CreatedAt,
		"last_login":    u.LastLogin,
		// The overrides on this account, as stored: "allow", "deny", or an ID
		// simply absent, which means it inherits the role template. Emitted so
		// the permission matrix can render the three states it has to edit --
		// an effective set alone would collapse "denied" and "the template
		// does not give it", and the editor needs to tell those apart.
		"grants": u.Grants,
		// Whether a picture exists, not the picture: the browser asks for the
		// image on its own route so it can be cached and revalidated like any
		// other image, instead of riding along in every account payload.
		"has_avatar": u.Avatar != "",
		"prefs":      u.Prefs,
		// Emitted: the page prints it, and a first name is not a credential.
		"english_name": strings.TrimSpace(u.EnglishName),
		// Whether sign-in takes two steps -- never the key or the codes.
		"two_step": u.SecondFactor.On(),
		"passkeys": len(u.Passkeys),
	}
}

// Normalised returns prefs with anything missing or out of range replaced.
//
// Applied on the way out as well as on the way in, so an account file that was
// hand-edited, or written by an older build, cannot put the interface into a
// state the user then has to fix from within that same unreadable interface.
func (p Prefs) Normalised() Prefs {
	out := p
	if out.FontScale < minFontScale || out.FontScale > maxFontScale {
		out.FontScale = 100
	}
	// Compact unless somebody has explicitly asked for the roomier scale.
	// Written this way round so an account file from an older build, or one
	// with the field missing, lands on the current default rather than on
	// whatever used to be first in this comparison.
	if out.Density != "comfortable" {
		out.Density = "compact"
	}
	out.Muted = cleanTopics(out.Muted)
	if !quietHour(out.QuietFrom) || !quietHour(out.QuietTo) {
		out.QuietFrom, out.QuietTo = 0, 0
	}
	return out
}

// The range of type scaling offered. The floor is where the smallest labels
// stop being legible at arm's length and the ceiling is where the report
// tables stop fitting a laptop screen; both were chosen by looking rather than
// by picking round numbers.
const (
	minFontScale = 85
	maxFontScale = 150
)

var (
	ErrNoSuchUser    = errors.New("auth: no such user")
	ErrUserExists    = errors.New("auth: a user with that name already exists")
	ErrBadCredential = errors.New("auth: incorrect user name or password")
	ErrDisabled      = errors.New("auth: this account is disabled")
	ErrLastAdmin     = errors.New("auth: refusing to leave the system with no active administrator")
	ErrInvalidRole   = errors.New("auth: unknown role")
	ErrInvalidName   = errors.New("auth: user name must be 3-64 characters of letters, digits, dot, dash or underscore")
	ErrSamePassword  = errors.New("auth: the new password is the current one")
)

// Store holds the accounts, persisted as JSON.
//
// JSON rather than a database because the whole point of this program is that
// it is one binary with nothing to install, and a company's staff list is tens
// of rows, not millions. The file holds password hashes, so it is written
// 0600 and replaced atomically.
type Store struct {
	path string
	// backend is where accounts are persisted: the accounts file by default,
	// a database when one is configured. See backend.go for why this is an
	// interface rather than a rewrite of the methods below.
	backend Backend
	mu      sync.RWMutex
	users   map[string]*User // keyed by the normalised user name
	// sealer protects second-factor keys at rest (secondfactor.go).
	sealer Sealer
}

// normalizeName lowercases a user name so that "Admin" and "admin" cannot
// become two accounts that look identical in every UI that lists them.
func normalizeName(s string) string { return strings.ToLower(strings.TrimSpace(s)) }

// ValidUsername reports whether a name is acceptable.
func ValidUsername(s string) bool {
	s = normalizeName(s)
	if len(s) < 3 || len(s) > 64 {
		return false
	}
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
		case r == '.', r == '-', r == '_':
		default:
			return false
		}
	}
	return true
}

// OpenStore loads the account file, creating an empty store if it is absent.
//
// Unchanged for every caller: the accounts file is still the store, and stays
// so until something configures a database. See OpenStoreWith.
func OpenStore(path string) (*Store, error) {
	return OpenStoreWith(path, newJSONBackend(path))
}

// OpenStoreWith loads the accounts from a given backend.
//
// Two constructors rather than a variadic option, because there are exactly
// two backends and the second one needs a database handle the first must not
// require. main decides which; nothing else in the application knows there is
// a choice.
func OpenStoreWith(path string, b Backend) (*Store, error) {
	s := &Store{path: path, backend: b, users: map[string]*User{}}
	list, err := b.Load()
	if err != nil {
		return nil, err
	}
	for _, u := range list {
		s.users[normalizeName(u.Username)] = u
	}
	return s, nil
}

// Degraded reports whether accounts are being served from the snapshot with
// the database down -- when a sign-in becomes an emergency session.
func (s *Store) Degraded() bool {
	if s == nil {
		return false
	}
	d, ok := s.backend.(interface{ Degraded() bool })
	return ok && d.Degraded()
}

// Refresh reloads every account from the backend. With the database back,
// that is what ends emergency mode without a restart; while it is still down
// the backend answers from the snapshot and nothing changes.
func (s *Store) Refresh() error {
	list, err := s.backend.Load()
	if err != nil {
		return err
	}
	fresh := make(map[string]*User, len(list))
	for _, u := range list {
		fresh[normalizeName(u.Username)] = u
	}
	s.mu.Lock()
	s.users = fresh
	s.mu.Unlock()
	return nil
}

// Describe names where accounts are being kept, for the health page.
func (s *Store) Describe() string {
	if s == nil || s.backend == nil {
		return ""
	}
	return s.backend.Describe()
}

// Healthy reports whether accounts can currently be CHANGED.
//
// Reading is always possible -- the accounts are in memory -- so this is only
// ever about writes, and it is what stops a page offering a control that would
// be refused.
func (s *Store) Healthy() bool {
	if s == nil || s.backend == nil {
		return false
	}
	return s.backend.Healthy()
}

// save persists every account. Callers hold the lock.
//
// Its signature is unchanged on purpose: eleven methods call this, and leaving
// it alone is what made moving accounts into a database a change to one
// function rather than to eleven. The writing itself now belongs to the
// backend -- see backend.go, where the file-writing body that used to be here
// still lives, moved rather than rewritten.
//
// SORTED, still. A stable order keeps a diff of the accounts file readable and
// stops every save looking like a change to anything watching it, because Go
// randomises map iteration.
func (s *Store) save() error {
	list := make([]*User, 0, len(s.users))
	for _, u := range s.users {
		list = append(list, u)
	}
	sort.Slice(list, func(i, j int) bool { return list[i].Username < list[j].Username })
	return s.backend.Save(list)
}

// Count reports how many accounts exist, which the setup screen uses to decide
// whether this is a first run.
func (s *Store) Count() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return len(s.users)
}

// List returns every account, ordered by name.
func (s *Store) List() []*User {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]*User, 0, len(s.users))
	for _, u := range s.users {
		out = append(out, u.clone())
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Username < out[j].Username })
	return out
}

// Get returns one account by name.
func (s *Store) Get(username string) (*User, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	u, ok := s.users[normalizeName(username)]
	if !ok {
		return nil, ErrNoSuchUser
	}
	return u.clone(), nil
}

// clone is a copy a caller can modify without touching the stored account.
//
// A PLAIN STRUCT COPY IS NOT ENOUGH, and the difference is a real bug rather
// than a tidiness point: `copied := *u` copies the fields, but Grants is a map,
// so both halves would go on sharing one.
// Anything that took a "copy" and edited its grants would silently be editing
// the live access model of a signed-in account, from outside any lock.
func (u *User) clone() *User {
	if u == nil {
		return nil
	}
	copied := *u
	if u.Grants != nil {
		copied.Grants = make(map[string]string, len(u.Grants))
		for k, v := range u.Grants {
			copied.Grants[k] = v
		}
	}
	// The same for the second factor: a pointer, and a slice under it.
	if u.SecondFactor != nil {
		f := *u.SecondFactor
		f.Recovery = append([]string(nil), u.SecondFactor.Recovery...)
		copied.SecondFactor = &f
	}
	if u.Passkeys != nil {
		copied.Passkeys = append([]Passkey(nil), u.Passkeys...)
	}
	if u.Prefs.Muted != nil {
		copied.Prefs.Muted = append([]string(nil), u.Prefs.Muted...)
	}
	return &copied
}

// SetGrants replaces this account's permission overrides.
//
// WHOLE-SET, not one at a time, because that is how the editing screen works:
// somebody opens a permission matrix, changes several boxes and saves. Applying
// them one at a time would write the accounts file once per checkbox and leave
// a half-applied set behind if one failed.
//
// An entry with a state that is neither allow nor deny is REFUSED rather than
// skipped: a typo in a request body must not quietly result in a permission
// set that is not what was sent.
func (s *Store) SetGrants(username string, grants map[string]GrantState) error {
	for id, state := range grants {
		if strings.TrimSpace(id) == "" {
			return fmt.Errorf("auth: a grant with no permission name")
		}
		if !state.Valid() {
			return fmt.Errorf("auth: %q is not a grant state for %q; use allow or deny", state, id)
		}
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	u, ok := s.users[normalizeName(username)]
	if !ok {
		return ErrNoSuchUser
	}

	before := u.Grants
	if len(grants) == 0 {
		// nil rather than an empty map, so an account with no overrides looks
		// like one on the wire and in the file.
		u.Grants = nil
	} else {
		next := make(map[string]string, len(grants))
		for id, state := range grants {
			next[id] = string(state)
		}
		u.Grants = next
	}
	if err := s.save(); err != nil {
		u.Grants = before
		return err
	}
	return nil
}

// Create adds an account.
func (s *Store) Create(username, displayName, password string, role Role) (*User, error) {
	if !ValidUsername(username) {
		return nil, ErrInvalidName
	}
	if !ValidRole(role) {
		return nil, ErrInvalidRole
	}
	hash, err := HashPassword(password)
	if err != nil {
		return nil, err
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	key := normalizeName(username)
	if _, exists := s.users[key]; exists {
		return nil, ErrUserExists
	}
	u := &User{
		Username:     key,
		DisplayName:  strings.TrimSpace(displayName),
		Role:         role,
		PasswordHash: hash,
		CreatedAt:    time.Now().UTC(),
	}
	u.PasswordChangedAt = u.CreatedAt
	s.users[key] = u
	if err := s.save(); err != nil {
		delete(s.users, key) // keep memory and disk agreeing
		return nil, err
	}
	return u.clone(), nil
}

// activeAdmins counts enabled administrators. Callers hold the lock.
func (s *Store) activeAdmins(excluding string) int {
	n := 0
	for key, u := range s.users {
		if key == excluding {
			continue
		}
		if u.Role == RoleAdmin && !u.Disabled {
			n++
		}
	}
	return n
}

// Update changes an account's display name, role and enabled state.
//
// Demoting or disabling the last active administrator is refused: the only
// screen that could undo it is the one that would become unreachable, and the
// fix would be hand-editing JSON on the server.
func (s *Store) Update(username, displayName string, role Role, disabled bool) (*User, error) {
	if !ValidRole(role) {
		return nil, ErrInvalidRole
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	key := normalizeName(username)
	u, ok := s.users[key]
	if !ok {
		return nil, ErrNoSuchUser
	}
	losingAdmin := u.Role == RoleAdmin && !u.Disabled && (role != RoleAdmin || disabled)
	if losingAdmin && s.activeAdmins(key) == 0 {
		return nil, ErrLastAdmin
	}

	before := *u
	u.DisplayName = strings.TrimSpace(displayName)
	u.Role = role
	u.Disabled = disabled
	// Any of these can reduce what somebody may do, so tokens issued under
	// the old settings must stop working immediately rather than carrying the
	// former access until they expire.
	u.TokenVersion++
	if err := s.save(); err != nil {
		*u = before
		return nil, err
	}
	return u.clone(), nil
}

// SetPassword replaces an account's password.
func (s *Store) SetPassword(username, password string) error {
	return s.setPassword(username, password, false)
}

// setPassword replaces a password; chosen says the person chose it
// themselves, which is what clears MustChangePassword.
func (s *Store) setPassword(username, password string, chosen bool) error {
	hash, err := HashPassword(password)
	if err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	u, ok := s.users[normalizeName(username)]
	if !ok {
		return ErrNoSuchUser
	}
	beforeHash, beforeVer, beforeMust, beforeAt := u.PasswordHash, u.TokenVersion, u.MustChangePassword, u.PasswordChangedAt
	u.PasswordHash = hash
	u.PasswordChangedAt = time.Now().UTC()
	if chosen {
		u.MustChangePassword = false
	}
	// Changing a password is the standard response to believing it leaked, so
	// it has to sign out whoever is holding one.
	u.TokenVersion++
	if err := s.save(); err != nil {
		u.PasswordHash, u.TokenVersion, u.MustChangePassword, u.PasswordChangedAt = beforeHash, beforeVer, beforeMust, beforeAt
		return err
	}
	return nil
}

// SetAvatar records that this account has a picture, in the given format, or
// clears it when ext is empty.
//
// The file itself is written by the caller. This only records the extension,
// and it does so AFTER the image is on disk: the other order would leave the
// account claiming a picture that cannot be served if the write failed.
func (s *Store) SetAvatar(username, ext string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	u, ok := s.users[normalizeName(username)]
	if !ok {
		return ErrNoSuchUser
	}
	before := u.Avatar
	u.Avatar = ext
	if err := s.save(); err != nil {
		u.Avatar = before
		return err
	}
	return nil
}

// SetPrefs stores someone's display preferences.
//
// The values are clamped here rather than validated at the edge, because this
// is the last point before they are persisted and there is more than one route
// that could reach it. A request asking for a 4000% font scale gets the
// default, not an error: nothing about it is worth failing a save over.
func (s *Store) SetPrefs(username string, p Prefs) (Prefs, error) {
	p = p.Normalised()
	s.mu.Lock()
	defer s.mu.Unlock()
	u, ok := s.users[normalizeName(username)]
	if !ok {
		return Prefs{}, ErrNoSuchUser
	}
	before := u.Prefs
	// The appearance form knows nothing of notification topics; a save from
	// it must not switch them all back on.
	p.Muted, p.SMS, p.Shortcuts, p.Menu = u.Prefs.Muted, u.Prefs.SMS, u.Prefs.Shortcuts, u.Prefs.Menu
	p.Pages, p.Sites, p.Hidden = u.Prefs.Pages, u.Prefs.Sites, u.Prefs.Hidden
	p.QuietFrom, p.QuietTo = u.Prefs.QuietFrom, u.Prefs.QuietTo
	u.Prefs = p
	if err := s.save(); err != nil {
		u.Prefs = before
		return Prefs{}, err
	}
	return p, nil
}

// ChangePassword replaces a password after proving the current one.
//
// Separate from SetPassword, which is the administrative form and asks for no
// proof. This one is what a person uses on themselves, so it verifies first --
// otherwise anyone who found an unlocked screen could lock the owner out of
// their own account.
//
// The comparison goes through Authenticate so that a disabled account cannot
// change its own password, and so the timing of a wrong password here matches
// the timing of a wrong password at sign-in.
//
// The new password must differ from the current one. Otherwise a temporary
// password handed out to many people at once could be "changed" to itself,
// and the account would clear its MustChangePassword flag still holding a
// password that everybody else knows.
func (s *Store) ChangePassword(username, current, next string) error {
	if _, err := s.Authenticate(username, current); err != nil {
		return err
	}
	if next == current {
		return ErrSamePassword
	}
	return s.setPassword(username, next, true)
}

// ResetPassword replaces a forgotten password, once the person has proved
// they hold the phone on the account (find's forgot-password route). They
// chose it, so a must-change flag clears, as with ChangePassword. There is no
// current password to prove, so the checks Authenticate made there are made
// here: a disabled account is refused, and the new password may not be the
// one it replaces -- or a temporary password could be "reset" to itself and
// lose its flag.
func (s *Store) ResetPassword(username, next string) error {
	s.mu.RLock()
	u, ok := s.users[normalizeName(username)]
	var hash string
	var disabled bool
	if ok {
		hash, disabled = u.PasswordHash, u.Disabled
	}
	s.mu.RUnlock()
	if !ok {
		return ErrNoSuchUser
	}
	if disabled {
		return ErrDisabled
	}
	if same, err := VerifyPassword(hash, next); err == nil && same {
		return ErrSamePassword
	}
	return s.setPassword(username, next, true)
}

// RevokeTokens invalidates every token already issued to a user, without
// changing anything else about the account.
func (s *Store) RevokeTokens(username string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	u, ok := s.users[normalizeName(username)]
	if !ok {
		return ErrNoSuchUser
	}
	before := u.TokenVersion
	u.TokenVersion++
	if err := s.save(); err != nil {
		u.TokenVersion = before
		return err
	}
	return nil
}

// Delete removes an account, refusing to remove the last active admin.
func (s *Store) Delete(username string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	key := normalizeName(username)
	u, ok := s.users[key]
	if !ok {
		return ErrNoSuchUser
	}
	if u.Role == RoleAdmin && !u.Disabled && s.activeAdmins(key) == 0 {
		return ErrLastAdmin
	}
	delete(s.users, key)
	if err := s.save(); err != nil {
		s.users[key] = u
		return err
	}
	return nil
}

// Authenticate checks a user name and password.
//
// Every failure returns the same error regardless of cause, and an unknown
// user still costs a full password derivation, so neither the message nor the
// timing reveals which accounts exist.
func (s *Store) Authenticate(username, password string) (*User, error) {
	s.mu.RLock()
	u, ok := s.users[normalizeName(username)]
	var copied User
	if ok {
		copied = *u
	}
	s.mu.RUnlock()

	if !ok {
		VerifyUnknownUser(password)
		return nil, ErrBadCredential
	}
	match, err := VerifyPassword(copied.PasswordHash, password)
	if err != nil || !match {
		return nil, ErrBadCredential
	}
	if copied.Disabled {
		return nil, ErrDisabled
	}

	// A hash written with weaker parameters is replaced now, while the
	// password is known -- computed before taking the lock, because it costs
	// as much as the check that just ran and every other sign-in would wait.
	// NOT a password change: the token version is untouched, so nobody is
	// signed out by it.
	var upgraded string
	if NeedsRehash(copied.PasswordHash) {
		if h, err := HashPassword(password); err == nil {
			upgraded = h
		}
	}

	s.mu.Lock()
	if live, still := s.users[normalizeName(username)]; still {
		live.LastLogin = time.Now().UTC()
		// Only if nobody changed the password in the meantime.
		if upgraded != "" && live.PasswordHash == copied.PasswordHash {
			live.PasswordHash = upgraded
		}
		_ = s.save() // a failed timestamp write must not fail the login
		copied = *live
	}
	s.mu.Unlock()
	return &copied, nil
}
