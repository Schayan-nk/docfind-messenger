package chat

import (
	"net/http"
	"strconv"
	"strings"
)

// The REST side of calls: one's call list («تماس‌ها»), rating a call that
// ended, declining from a notification, and taking a call off one's list.

// CallList is GET /api/chat/calls?before=<id>: fifty at a time, newest first.
func (a *API) CallList(w http.ResponseWriter, r *http.Request) {
	before, _ := strconv.ParseInt(r.URL.Query().Get("before"), 10, 64)
	if before < 0 {
		before = 0
	}
	list, err := a.Svc.Store.Calls(r.Context(), a.User(r), before, 50)
	if err != nil {
		writeErr(w, err)
		return
	}
	for i := range list {
		if list[i].Peer.ID == "" {
			list[i].Peer.Name = "حساب حذف‌شده"
		} else {
			list[i].Peer.Name, list[i].Peer.Photo = a.Svc.displayName(list[i].Peer.ID), a.Svc.hasPhoto(list[i].Peer.ID)
		}
	}
	if list == nil {
		list = []CallEntry{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"calls": list})
}

// CallRate is POST /api/chat/calls/{id}/rate {"stars": 1-5, "note"}: how the
// call was, kept for diagnosing the network.
func (a *API) CallRate(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Stars int    `json:"stars"`
		Note  string `json:"note"`
	}
	if !decode(w, r, &body) {
		return
	}
	note := strings.TrimSpace(CleanText(body.Note))
	if body.Stars < 1 || body.Stars > 5 || UTF16Len(note) > 500 {
		writeErr(w, ErrEmpty)
		return
	}
	ok, err := a.Svc.Store.CallRate(r.Context(), pathID(r, "id"), a.User(r), body.Stars, note)
	if err == nil && !ok {
		err = ErrNoCall
	}
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

// CallDeclineREST is POST /api/chat/calls/{id}/decline {"text"?}: a ringing
// call refused from its notification.
func (a *API) CallDeclineREST(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Text string `json:"text"`
	}
	if !decode(w, r, &body) {
		return
	}
	if err := a.Svc.CallDecline(r.Context(), a.User(r), pathID(r, "id"), body.Text); err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

// CallHideREST is POST /api/chat/calls/{id}/hide: off one's own list.
func (a *API) CallHideREST(w http.ResponseWriter, r *http.Request) {
	if err := a.Svc.Store.CallHide(r.Context(), pathID(r, "id"), a.User(r)); err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}
