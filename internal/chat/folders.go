package chat

import (
	"context"
	"encoding/json"
	"errors"
	"strings"

	"github.com/jackc/pgx/v5"
)

// Chat folders (SPEC 6.5): a person's own tabs over their chat list. The rules
// (which kinds of chat, muted, read, archived) are applied by the page to the
// list it already has; the server keeps the folder and its chosen chats, and
// only ever chats the person is in. A folder of groups and channels can be
// shared: the link adds the folder and joins its chats through their own
// invite links, so bans and approval still apply.

var ErrFolderLimit = errors.New("chat: too many folders")

const (
	maxFolders      = 20
	maxFolderChats  = 200
	maxFolderPinned = 20
)

// FolderRules: include (contacts, non_contacts, groups, channels) or exclude
// (muted, read, archived) chats by kind.
type FolderRules struct {
	Contacts    bool `json:"contacts,omitempty"`
	NonContacts bool `json:"non_contacts,omitempty"`
	Groups      bool `json:"groups,omitempty"`
	Channels    bool `json:"channels,omitempty"`
	Muted       bool `json:"muted,omitempty"`
	Read        bool `json:"read,omitempty"`
	Archived    bool `json:"archived,omitempty"`
}

// Folder is one of a person's folders.
type Folder struct {
	ID           int64       `json:"id"`
	Title        string      `json:"title"`
	Icon         string      `json:"icon,omitempty"`
	Include      FolderRules `json:"include"`
	Exclude      FolderRules `json:"exclude"`
	IncludeChats []int64     `json:"include_chats"`
	ExcludeChats []int64     `json:"exclude_chats"`
	Pinned       []int64     `json:"pinned"`
	Token        string      `json:"token,omitempty"` // its share link, when shared
}

const folderCols = `id, title, icon, include_rules, exclude_rules, include_chats, exclude_chats, pinned_chats, COALESCE(share_token, '')`

func scanFolder(r pgx.Row) (*Folder, error) {
	f := &Folder{}
	var inc, exc []byte
	if err := r.Scan(&f.ID, &f.Title, &f.Icon, &inc, &exc, &f.IncludeChats, &f.ExcludeChats, &f.Pinned, &f.Token); err != nil {
		return nil, err
	}
	_ = json.Unmarshal(inc, &f.Include)
	_ = json.Unmarshal(exc, &f.Exclude)
	return f, nil
}

// Folders lists user's folders in their order.
func (s *Store) Folders(ctx context.Context, user string) ([]*Folder, error) {
	rows, err := s.pool.Query(ctx, `SELECT `+folderCols+` FROM chat_folders WHERE user_id = $1 ORDER BY position, id`, user)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []*Folder{}
	for rows.Next() {
		f, err := scanFolder(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, f)
	}
	return out, rows.Err()
}

// mine keeps the chats of ids that user is in, in their order, without repeats.
func (s *Store) mine(ctx context.Context, user string, ids []int64, max int) ([]int64, error) {
	if len(ids) == 0 {
		return []int64{}, nil
	}
	if len(ids) > max {
		ids = ids[:max]
	}
	rows, err := s.pool.Query(ctx, `SELECT o.chat_id FROM unnest($2::bigint[]) WITH ORDINALITY AS o(chat_id, n)
		JOIN chat_members m ON m.chat_id = o.chat_id AND m.user_id = $1 AND m.role NOT IN ('left', 'banned')
		ORDER BY o.n`, user, ids)
	if err != nil {
		return nil, err
	}
	got, err := pgx.CollectRows(rows, pgx.RowTo[int64])
	if err != nil {
		return nil, err
	}
	seen, out := map[int64]bool{}, []int64{}
	for _, id := range got {
		if !seen[id] {
			seen[id] = true
			out = append(out, id)
		}
	}
	return out, nil
}

// SaveFolder makes (ID 0) or changes one of user's folders.
func (s *Store) SaveFolder(ctx context.Context, user string, f *Folder) (*Folder, error) {
	title := strings.TrimSpace(CleanText(f.Title))
	// The icon is an emoji or a short label, rendered by the client. Reject
	// angle brackets so it can never be stored as markup: the folder/topic icon
	// is the one user string the client may render as raw SVG (chatstate.js
	// svg()), and this is the server-side half of keeping that from becoming
	// stored XSS no matter which write path or future client reaches it.
	if title == "" || UTF16Len(title) > 32 || UTF16Len(f.Icon) > 8 || strings.ContainsAny(f.Icon, "<>") {
		return nil, ErrEmpty
	}
	var err error
	if f.IncludeChats, err = s.mine(ctx, user, f.IncludeChats, maxFolderChats); err != nil {
		return nil, err
	}
	if f.ExcludeChats, err = s.mine(ctx, user, f.ExcludeChats, maxFolderChats); err != nil {
		return nil, err
	}
	if f.Pinned, err = s.mine(ctx, user, f.Pinned, maxFolderPinned); err != nil {
		return nil, err
	}
	inc, _ := json.Marshal(f.Include)
	exc, _ := json.Marshal(f.Exclude)
	if f.ID == 0 {
		var n int
		if err := s.pool.QueryRow(ctx, `SELECT count(*) FROM chat_folders WHERE user_id = $1`, user).Scan(&n); err != nil {
			return nil, err
		}
		if n >= maxFolders {
			return nil, ErrFolderLimit
		}
		return scanFolder(s.pool.QueryRow(ctx, `INSERT INTO chat_folders (user_id, title, icon, include_rules, exclude_rules, include_chats, exclude_chats, pinned_chats, position)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9) RETURNING `+folderCols, user, title, f.Icon, inc, exc, f.IncludeChats, f.ExcludeChats, f.Pinned, n))
	}
	out, err := scanFolder(s.pool.QueryRow(ctx, `UPDATE chat_folders SET title = $3, icon = $4, include_rules = $5, exclude_rules = $6,
			include_chats = $7, exclude_chats = $8, pinned_chats = $9
		WHERE id = $1 AND user_id = $2 RETURNING `+folderCols, f.ID, user, title, f.Icon, inc, exc, f.IncludeChats, f.ExcludeChats, f.Pinned))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotMember
	}
	return out, err
}

// DeleteFolder removes one of user's folders (its chats stay where they are).
func (s *Store) DeleteFolder(ctx context.Context, user string, id int64) error {
	t, err := s.pool.Exec(ctx, `DELETE FROM chat_folders WHERE id = $1 AND user_id = $2`, id, user)
	if err == nil && t.RowsAffected() == 0 {
		return ErrNotMember
	}
	return err
}

// OrderFolders puts user's folders in the order given.
func (s *Store) OrderFolders(ctx context.Context, user string, ids []int64) error {
	if len(ids) > maxFolders {
		return ErrForbidden
	}
	_, err := s.pool.Exec(ctx, `UPDATE chat_folders f SET position = o.n
		FROM unnest($2::bigint[]) WITH ORDINALITY AS o(id, n) WHERE f.id = o.id AND f.user_id = $1`, user, ids)
	return err
}

// ShareFolder gives one of user's folders a link (the same one each time).
func (s *Store) ShareFolder(ctx context.Context, user string, id int64) (string, error) {
	var tok string
	err := s.pool.QueryRow(ctx, `UPDATE chat_folders SET share_token = COALESCE(share_token, $3)
		WHERE id = $1 AND user_id = $2 RETURNING share_token`, id, user, newToken()).Scan(&tok)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", ErrNotMember
	}
	return tok, err
}

// SharedChat is a chat a shared folder offers: a group or channel its owner is
// in, with a primary invite link to join it by.
type SharedChat struct {
	ID      int64  `json:"id"`
	Title   string `json:"title"`
	Type    string `json:"type"`
	Members int    `json:"members"`
	link    string
}

// folderByToken is a shared folder and what it offers.
func (s *Store) folderByToken(ctx context.Context, token string) (*Folder, []SharedChat, error) {
	var owner string
	f := &Folder{}
	var inc, exc []byte
	err := s.pool.QueryRow(ctx, `SELECT user_id, `+folderCols+` FROM chat_folders WHERE share_token = $1`, token).
		Scan(&owner, &f.ID, &f.Title, &f.Icon, &inc, &exc, &f.IncludeChats, &f.ExcludeChats, &f.Pinned, &f.Token)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil, ErrLinkGone
	}
	if err != nil {
		return nil, nil, err
	}
	rows, err := s.pool.Query(ctx, `SELECT c.id, c.title, c.type,
			(SELECT count(*) FROM chat_members x WHERE x.chat_id = c.id AND x.role NOT IN ('left', 'banned')), l.token
		FROM unnest($2::bigint[]) WITH ORDINALITY AS o(chat_id, n)
		JOIN chats c ON c.id = o.chat_id AND c.type IN ('group', 'supergroup', 'channel')
		JOIN chat_members m ON m.chat_id = c.id AND m.user_id = $1 AND m.role NOT IN ('left', 'banned')
		JOIN chat_invite_links l ON l.chat_id = c.id AND l.is_primary AND NOT l.revoked
		ORDER BY o.n`, owner, f.IncludeChats)
	if err != nil {
		return nil, nil, err
	}
	defer rows.Close()
	var out []SharedChat
	for rows.Next() {
		var c SharedChat
		if err := rows.Scan(&c.ID, &c.Title, &c.Type, &c.Members, &c.link); err != nil {
			return nil, nil, err
		}
		out = append(out, c)
	}
	return f, out, rows.Err()
}

// ---------------------------------------------------------------- service

// FolderPreview is what a folder link shows before adding it.
func (s *Service) FolderPreview(ctx context.Context, token string) (map[string]any, error) {
	f, chats, err := s.Store.folderByToken(ctx, token)
	if err != nil {
		return nil, err
	}
	if chats == nil {
		chats = []SharedChat{}
	}
	return map[string]any{"title": f.Title, "icon": f.Icon, "chats": chats}, nil
}

// JoinFolder adds a shared folder to user's list and joins its chats through
// their invite links (a chat that needs approval gets a request; a ban keeps
// its chat out). It answers with the new folder and how many chats were joined.
func (s *Service) JoinFolder(ctx context.Context, user, token string) (*Folder, int, error) {
	f, chats, err := s.Store.folderByToken(ctx, token)
	if err != nil {
		return nil, 0, err
	}
	var in []int64
	joined := 0
	for _, c := range chats {
		m, _ := s.Store.MemberOf(ctx, c.ID, user)
		if m != nil && m.Role != "left" && m.Role != "banned" {
			in = append(in, c.ID)
			continue
		}
		if id, err := s.JoinByLink(ctx, user, c.link); err == nil && id > 0 {
			in = append(in, id)
			joined++
		}
	}
	nf, err := s.Store.SaveFolder(ctx, user, &Folder{Title: f.Title, Icon: f.Icon, IncludeChats: in})
	if err != nil {
		return nil, joined, err
	}
	s.deliver([]string{user}, "folders", map[string]any{}, nil)
	return nf, joined, nil
}
