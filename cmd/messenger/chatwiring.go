package main

import (
	"context"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/Schayan-nk/docfind-messenger/internal/auth"
	"github.com/Schayan-nk/docfind-messenger/internal/calls"
	"github.com/Schayan-nk/docfind-messenger/internal/chat"
	"github.com/Schayan-nk/docfind-messenger/internal/find"
	"github.com/Schayan-nk/docfind-messenger/internal/pgstore"
)

// startChat turns the messenger on when PostgreSQL is configured. The tables
// come from migration 044, applied with the rest at start-up.
func startChat(app *find.Server, db *pgstore.DB, users *auth.Store, signer *auth.Signer, stateDir string) {
	if db == nil {
		logf("messenger is off: it needs PostgreSQL (DOCFIND_PG_*), which holds every chat")
		return
	}
	svc := chat.New(chat.NewStore(db.Pool()), users, signer)
	svc.Logf = logf
	// Files sent in chats: DOCFIND_CHAT_MEDIA, else the folder an
	// administrator chose in the settings, else chat-media beside the accounts.
	if folder, err := chat.OpenMediaFolder(filepath.Join(stateDir, "chat-media"), filepath.Join(stateDir, "chat-media.json")); err != nil {
		logf("messenger: files are off: %v", err)
	} else {
		root, from := folder.Root()
		if err := chat.CheckWritable(root); err != nil {
			logf("messenger: the chat media folder %s (%s) cannot be used: %v", root, from, err)
		}
		svc.Media = folder
		logf("messenger: chat files kept in %s (%s)", root, from)
	}
	svc.AllowExecutables = os.Getenv("DOCFIND_CHAT_ALLOW_EXECUTABLES") == "1"
	sfuPort := startSFU(svc)
	startTURN(svc, users, sfuPort)
	go svc.Run(context.Background())
	app.UseChat(svc)
	logf("messenger on: /messages, socket /api/chat/ws")
}

// startTURN runs docfind's own STUN and TURN server for calls when
// DOCFIND_TURN_SECRET is set (internal/calls). Without it calls still work
// where the two browsers reach each other directly (one office network).
func startTURN(svc *chat.Service, users *auth.Store, sfuPort int) {
	cfg, on, err := calls.FromEnv()
	if err != nil {
		logf("calls: TURN is off: %v", err)
		return
	}
	if !on {
		logf("calls: no TURN server (DOCFIND_TURN_SECRET not set); calls connect only where the two browsers reach each other")
		return
	}
	// A relay works only for an active account in the session it was given
	// to: disabling the account or ending its sessions stops it.
	if sfuPort > 0 {
		cfg.OwnPorts = append(cfg.OwnPorts, sfuPort) // relayed browsers reach the group calls' media server
	}
	active := func(user string, session int) bool {
		u, err := users.Get(user)
		return err == nil && !u.Disabled && u.TokenVersion == session
	}
	srv, err := calls.Start(cfg, active, logf)
	if err != nil {
		logf("calls: TURN is off: %v", err)
		return
	}
	c := srv.Config()
	svc.ICE = func(user string, session int, host string) []chat.ICEServer {
		list := srv.ICE(user, session, host)
		out := make([]chat.ICEServer, len(list))
		for i, x := range list {
			out[i] = chat.ICEServer{URLs: x.URLs, Username: x.Username, Credential: x.Credential}
		}
		return out
	}
	logf("calls: TURN on UDP and TCP %d, relays at %s ports %d-%d", c.Port, c.RelayIP, c.MinPort, c.MaxPort)
}

// startSFU runs the group calls' media server (internal/calls/sfu.go) on
// UDP and TCP DOCFIND_SFU_PORT (3479), announcing DOCFIND_SFU_IP, else the
// TURN relay's address, else this machine's office address. DOCFIND_SFU=off
// turns group calls off. Returns the port, or 0.
func startSFU(svc *chat.Service) int {
	if strings.EqualFold(strings.TrimSpace(os.Getenv("DOCFIND_SFU")), "off") {
		logf("calls: group calls are off (DOCFIND_SFU=off)")
		return 0
	}
	cfg := calls.SFUConfig{Port: 3479}
	if p := strings.TrimSpace(os.Getenv("DOCFIND_SFU_PORT")); p != "" {
		n, err := strconv.Atoi(p)
		if err != nil || n < 1 || n > 65535 {
			logf("calls: group calls are off: DOCFIND_SFU_PORT %q is not a port", p)
			return 0
		}
		cfg.Port = n
	}
	for _, v := range []string{"DOCFIND_SFU_IP", "DOCFIND_TURN_RELAY_IP"} {
		if ip := net.ParseIP(strings.TrimSpace(os.Getenv(v))).To4(); ip != nil {
			cfg.PublicIP = ip
			break
		}
	}
	if cfg.PublicIP == nil {
		cfg.PublicIP = calls.OfficeIPv4()
	}
	srv, err := calls.StartSFU(cfg, logf)
	if err != nil {
		logf("calls: group calls are off: %v", err)
		return 0
	}
	svc.GroupMedia = sfuMedia{srv}
	logf("calls: group calls' media server on UDP and TCP %d at %s", srv.Port(), cfg.PublicIP)
	return srv.Port()
}

// sfuMedia lends the media server to the chat package.
type sfuMedia struct{ s *calls.SFU }

func (m sfuMedia) Join(room int64, user string, send func(map[string]any), gone func()) (chat.MediaPeer, error) {
	p, err := m.s.Join(room, user, send, gone)
	if err != nil {
		return nil, err
	}
	return p, nil
}
func (m sfuMedia) Mute(room int64, user string, on bool) { m.s.Mute(room, user, on) }
func (m sfuMedia) End(room int64)                        { m.s.End(room) }
