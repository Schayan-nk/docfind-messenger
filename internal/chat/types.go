package chat

import (
	"encoding/json"
	"errors"
	"time"
)

// Envelope is every WebSocket frame, both directions: {"t":"type","id":"...","d":{...}}.
// id is the client's id for a request; a reply carries it back.
type Envelope struct {
	T  string          `json:"t"`
	ID string          `json:"id,omitempty"`
	D  json.RawMessage `json:"d,omitempty"`
}

// Person is how one member is shown to another. Presence follows the
// target's privacy setting (see presenceFor).
type Person struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	Handle   string `json:"handle,omitempty"`
	Online   bool   `json:"online"`
	LastSeen string `json:"last_seen,omitempty"` // RFC 3339, only when the target shares it
	Seen     string `json:"seen,omitempty"`      // "recently" | "week" | "month" | "long" when the exact time is hidden
	Photo    bool   `json:"photo,omitempty"`     // their DocFind picture exists (/api/account/photo?u=)
}

// Message is one message as the page sees it.
type Message struct {
	ID       int64           `json:"id"`
	ChatID   int64           `json:"chat"`
	PTS      int64           `json:"pts"`
	Sender   string          `json:"from,omitempty"`
	ClientID string          `json:"cid,omitempty"`
	Kind     string          `json:"kind"`
	Text     string          `json:"text"`
	Entities json.RawMessage `json:"entities,omitempty"`
	ReplyTo  *int64          `json:"reply_to,omitempty"`
	Edited   *time.Time      `json:"edited,omitempty"`
	Deleted  bool            `json:"deleted,omitempty"`
	Silent   bool            `json:"silent,omitempty"`
	Created  time.Time       `json:"at"`

	// Phase 2. Reply is the replied-to message as this chat sees it (same chat
	// only; a reply from another chat carries its snapshot in Quote instead).
	Reply     *ReplyPreview   `json:"reply,omitempty"`
	Quote     json.RawMessage `json:"quote,omitempty"`
	Fwd       json.RawMessage `json:"fwd,omitempty"`
	Reactions []ReactionCount `json:"reactions,omitempty"`
	Mine      []string        `json:"mine,omitempty"` // the reader's own reactions
	Pinned    bool            `json:"pinned,omitempty"`

	// Phase 3. MediaID is the stored file; Media its details, filled in for
	// the reader. Album groups the messages sent together; Preview is the
	// link preview; DocRef a DocFind document, shown as Doc to a reader who
	// may open it (and as {"denied":true} to one who may not).
	MediaID *int64          `json:"-"`
	Media   *Media          `json:"media,omitempty"`
	Album   string          `json:"album,omitempty"`
	Preview json.RawMessage `json:"preview,omitempty"`
	DocRef  string          `json:"-"`
	Doc     any             `json:"doc,omitempty"`

	// Phase 4: the forum topic (0: General), what a service message says
	// happened ("X added Y"), and a channel post's views.
	Topic    int64           `json:"topic,omitempty"`
	Service  json.RawMessage `json:"service,omitempty"`
	Views    int             `json:"views,omitempty"`
	FromName string          `json:"from_name,omitempty"` // the sender as shown, in groups and channels

	// Phase 11: a poll, location, sticker or GIF message's own data.
	Extra json.RawMessage `json:"extra,omitempty"`
}

// ReplyPreview is the strip above a reply: who wrote the original and how it began.
type ReplyPreview struct {
	ID      int64  `json:"id"`
	PTS     int64  `json:"pts"`
	From    string `json:"from,omitempty"`
	Text    string `json:"text"`
	Deleted bool   `json:"deleted,omitempty"`
}

// Update is one row of a chat's change log (chat_updates).
type Update struct {
	ChatID  int64           `json:"chat"`
	PTS     int64           `json:"pts"`
	Kind    string          `json:"kind"`
	Payload json.RawMessage `json:"d"`
	At      time.Time       `json:"at"`
}

// Chat is one row of the chat list.
type Chat struct {
	ID          int64    `json:"id"`
	Type        string   `json:"type"`
	Title       string   `json:"title"`
	Peer        *Person  `json:"peer,omitempty"`
	PTS         int64    `json:"pts"`
	ReadPTS     int64    `json:"read_pts"`      // what I have read
	PeerReadPTS int64    `json:"peer_read_pts"` // what the other side has read (my ticks)
	Unread      int      `json:"unread"`
	Last        *Message `json:"last,omitempty"`
	Role        string   `json:"role"`
	Draft       string   `json:"draft,omitempty"`
	Mentions    int      `json:"mentions,omitempty"` // unread messages that mention me
	Forum       bool     `json:"forum,omitempty"`
	// Phase 5: my own handling of the chat, and what the page's folder rules need.
	Pinned       int        `json:"pinned,omitempty"` // order among pinned chats
	Archived     bool       `json:"archived,omitempty"`
	MutedUntil   *time.Time `json:"muted_until,omitempty"`
	MentionsOnly bool       `json:"mentions_only,omitempty"`
	MarkedUnread bool       `json:"marked_unread,omitempty"`
	Reactions    int        `json:"reactions,omitempty"` // reactions to my messages I have not seen
	Contact      bool       `json:"contact,omitempty"`   // a private chat with one of my contacts
	Sound        string     `json:"sound,omitempty"`     // this chat's in-app sound (phase 6)
}

// Errors the socket and the REST API both translate into Persian for the page.
var (
	ErrNotMember = errors.New("chat: not a member")
	ErrForbidden = errors.New("chat: not allowed")
	ErrBlocked   = errors.New("chat: blocked")
	ErrEmpty     = errors.New("chat: empty message")
	ErrBadID     = errors.New("chat: bad client message id")
	ErrNoUser    = errors.New("chat: no such user")
	ErrRate      = errors.New("chat: too many messages")
	ErrTooLong   = errors.New("chat: text too long")
)

// errorText is what the page shows for each error: Persian, no internals.
func errorText(err error) (code, text string) {
	switch {
	case errors.Is(err, ErrNotMember):
		return "not_member", "شما عضو این گفت‌وگو نیستید."
	case errors.Is(err, ErrForbidden):
		return "forbidden", "اجازهٔ این کار را ندارید."
	case errors.Is(err, ErrBlocked):
		return "blocked", "امکان ارسال پیام به این کاربر وجود ندارد."
	case errors.Is(err, ErrEmpty):
		return "empty", "پیام خالی است."
	case errors.Is(err, ErrBadID):
		return "bad_id", "شناسهٔ پیام نامعتبر است؛ صفحه را دوباره باز کنید."
	case errors.Is(err, ErrNoUser):
		return "no_user", "این کاربر پیدا نشد."
	case errors.Is(err, ErrPinLimit):
		return "pin_limit", "بیش از این نمی‌توان گفت‌وگو سنجاق کرد؛ یکی را بردارید."
	case errors.Is(err, ErrFolderLimit):
		return "folder_limit", "بیش از ۲۰ پوشه نمی‌توان ساخت."
	case errors.Is(err, ErrSlowMode):
		return "slow_mode", "حالت آهسته روشن است؛ کمی بعد دوباره بفرستید."
	case errors.Is(err, ErrNewLinks):
		return "new_links", "اعضای تازه هنوز نمی‌توانند پیوند بفرستند."
	case errors.Is(err, ErrClosed):
		return "closed", "این موضوع بسته است."
	case errors.Is(err, ErrOwnerLeaves):
		return "owner", "پیش از ترک گفت‌وگو، مالکیت آن را به عضو دیگری بسپارید."
	case errors.Is(err, ErrPrivacy):
		return "privacy", "تنظیمات حریم خصوصی این همکار اجازه نمی‌دهد؛ پیوند دعوت را برایش بفرستید."
	case errors.Is(err, ErrFull):
		return "full", "ظرفیت این گفت‌وگو پر است."
	case errors.Is(err, ErrPassword):
		return "password", "گذرواژه درست نیست."
	case errors.Is(err, ErrLinkGone):
		return "link_gone", "این پیوند دعوت دیگر معتبر نیست."
	case errors.Is(err, ErrBanned):
		return "banned", "شما از این گفت‌وگو کنار گذاشته شده‌اید."
	case errors.Is(err, ErrPending):
		return "pending", "درخواست عضویت فرستاده شد؛ پس از تأیید مدیر عضو می‌شوید."
	case errors.Is(err, ErrTooLong):
		return "too_long", "متن پیام بیش از ۴٬۰۹۶ نویسه است."
	case errors.Is(err, ErrEntities):
		return "bad_format", "قالب‌بندی پیام نامعتبر است."
	case errors.Is(err, ErrRate):
		return "rate", "پیام‌ها خیلی سریع ارسال شدند؛ چند لحظه صبر کنید."
	case errors.Is(err, ErrNoCall):
		return "no_call", "این تماس دیگر برقرار نیست."
	case errors.Is(err, ErrInCall):
		return "in_call", "شما در تماس دیگری هستید؛ نخست آن را پایان دهید."
	case errors.Is(err, ErrNoGroupCalls):
		return "no_group_calls", "تماس گروهی روی این سرور فعال نیست."
	case errors.Is(err, ErrCallFull):
		return "call_full", "ظرفیت این تماس گروهی پر است."
	case errors.Is(err, ErrCallPrivacy):
		return "call_privacy", "تنظیمات حریم خصوصی این همکار اجازهٔ تماس نمی‌دهد."
	}
	return "server", "خطایی پیش آمد؛ دوباره تلاش کنید."
}
