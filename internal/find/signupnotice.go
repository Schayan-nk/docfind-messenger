package find

import (
	"context"
	"fmt"
	"time"

	"github.com/Schayan-nk/docfind-messenger/internal/auth"
)

// announceSignup tells whoever made the invite that somebody has used it: a
// line in a private chat with the newcomer and a notification on their
// devices (chat.Service.Joined). Runs off the request, so a slow database
// never holds up the new account's first page.
func (s *Server) announceSignup(newcomer, name, owner string) {
	if s.chat == nil {
		return
	}
	to := s.signupAudience(owner, newcomer)
	if len(to) == 0 {
		return
	}
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		for _, u := range to {
			if err := s.chat.Joined(ctx, u, newcomer, name); err != nil {
				fmt.Printf("sign-up notice to %s failed: %v\n", u, err)
			}
		}
	}()
}

// signupAudience is the invite's maker while that account can still sign in;
// otherwise every administrator, so a sign-up is never announced to nobody.
func (s *Server) signupAudience(owner, newcomer string) []string {
	if owner != "" && owner != newcomer {
		if u, err := s.guard.Users.Get(owner); err == nil && !u.Disabled {
			return []string{u.Username}
		}
	}
	var out []string
	for _, u := range s.guard.Users.List() {
		if u.Role == auth.RoleAdmin && !u.Disabled && u.Username != newcomer {
			out = append(out, u.Username)
		}
	}
	return out
}
