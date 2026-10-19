package chat

import (
	"context"
	"net/http"
	"time"

	"github.com/jackc/pgx/v5"
)

// Group and channel statistics for admins (SPEC 6.7): member growth,
// messages per day, top posters, and a channel's views, over 30 days. The
// page draws them as plain SVG bars (chatgroupadmin.js).

// DayCount is one day's number.
type DayCount struct {
	Day time.Time `json:"day"`
	N   int       `json:"n"`
}

// Poster is one member's message count.
type Poster struct {
	User string `json:"user"`
	Name string `json:"name"`
	N    int    `json:"n"`
}

func dayCounts(ctx context.Context, s *Store, q string, chatID int64) ([]DayCount, error) {
	rows, err := s.pool.Query(ctx, q, chatID)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(r pgx.CollectableRow) (DayCount, error) {
		var d DayCount
		err := r.Scan(&d.Day, &d.N)
		return d, err
	})
}

// GroupStats is GET /api/chat/chats/{id}/stats.
func (a *API) GroupStats(w http.ResponseWriter, r *http.Request) {
	ctx, me, id := r.Context(), a.User(r), pathID(r, "id")
	info, m, err := a.Svc.groupCtx(ctx, id, me)
	if err != nil {
		writeErr(w, err)
		return
	}
	if m.Role != "owner" && m.Role != "admin" {
		writeErr(w, ErrForbidden)
		return
	}
	s := a.Svc.Store
	joins, err := dayCounts(ctx, s, `SELECT date_trunc('day', joined_at), count(*) FROM chat_members
		WHERE chat_id = $1 AND joined_at > now() - interval '30 days' GROUP BY 1 ORDER BY 1`, id)
	if err != nil {
		writeErr(w, err)
		return
	}
	perDay, err := dayCounts(ctx, s, `SELECT date_trunc('day', created_at), count(*) FROM chat_messages
		WHERE chat_id = $1 AND created_at > now() - interval '30 days' AND kind <> 'service' AND deleted_at IS NULL GROUP BY 1 ORDER BY 1`, id)
	if err != nil {
		writeErr(w, err)
		return
	}
	rows, err := s.pool.Query(ctx, `SELECT COALESCE(sender_id, ''), count(*) FROM chat_messages
		WHERE chat_id = $1 AND created_at > now() - interval '30 days' AND kind <> 'service' AND deleted_at IS NULL
		GROUP BY 1 ORDER BY 2 DESC LIMIT 10`, id)
	if err != nil {
		writeErr(w, err)
		return
	}
	top, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (Poster, error) {
		var p Poster
		err := r.Scan(&p.User, &p.N)
		return p, err
	})
	if err != nil {
		writeErr(w, err)
		return
	}
	for i := range top {
		top[i].Name = top[i].User
		if u, err := a.Svc.users.Get(top[i].User); err == nil && u.DisplayName != "" {
			top[i].Name = u.DisplayName
		}
	}
	out := map[string]any{"members": info.Members, "joins": joins, "messages": perDay, "top": top}
	if info.Type == "channel" {
		posts, err := s.Page(ctx, id, me, PageQuery{Limit: 20})
		if err == nil {
			s.Views(ctx, id, posts)
			type pv struct {
				ID    int64  `json:"id"`
				Text  string `json:"text"`
				Views int    `json:"views"`
			}
			var views []pv
			for _, p := range posts {
				views = append(views, pv{p.ID, preview(p.Text), p.Views})
			}
			out["views"] = views
		}
	}
	writeJSON(w, http.StatusOK, out)
}
