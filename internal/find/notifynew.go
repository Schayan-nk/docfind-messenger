package find

import (
	"github.com/Schayan-nk/docfind-messenger/internal/auth"
)

// newTopicReaches is topicReaches for the messenger's topic; ok says whether
// id is one this server knows.
func (s *Server) newTopicReaches(_ *auth.User, id string) (reach, ok bool) {
	if id == topicChat {
		return s.chat != nil, true
	}
	return false, false
}
