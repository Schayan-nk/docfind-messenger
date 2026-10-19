package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/coder/websocket"
	"github.com/pion/stun/v3"

	"github.com/Schayan-nk/docfind-messenger/internal/calls"
	"github.com/Schayan-nk/docfind-messenger/internal/chat"
	"github.com/Schayan-nk/docfind-messenger/internal/pgstore"
)

// checkChat is -check-chat: one PASS/FAIL line per thing the messenger needs,
// then a non-nil error if anything required failed. Run it on the server after
// a deploy (docs/chat/SPEC.md, phase 0 item 5).
func checkChat(port int, stateDir string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	failed := 0
	line := func(ok bool, item, detail string) {
		tag := "PASS"
		if !ok {
			tag = "FAIL"
			failed++
		}
		fmt.Printf("%-4s  %-12s %s\n", tag, item, detail)
	}
	info := func(item, detail string) { fmt.Printf("%-4s  %-12s %s\n", "--", item, detail) }

	// PostgreSQL and the chat tables (migration 044).
	if c := pgstore.FromEnv(); !c.Enabled() {
		line(false, "postgresql", "DOCFIND_PG_* is not set")
	} else if db, err := pgstore.Open(ctx, c); err != nil {
		line(false, "postgresql", "cannot connect: "+err.Error())
	} else {
		var n int
		err := db.Pool().QueryRow(ctx, `SELECT count(*) FROM chats`).Scan(&n)
		if err != nil {
			line(false, "postgresql", "connected, but the chat tables are missing (start docfind once to apply migration 044)")
		} else {
			line(true, "postgresql", fmt.Sprintf("connected; chat tables present (%d chats)", n))
		}
		db.Close()
	}

	// ffmpeg (needed from phase 3: thumbnails, waveforms, sticker conversion, recordings).
	if p := os.Getenv("DOCFIND_FFMPEG"); p == "" {
		info("ffmpeg", "DOCFIND_FFMPEG not set (needed from the media phase on)")
	} else if out, err := exec.CommandContext(ctx, p, "-version").Output(); err != nil {
		line(false, "ffmpeg", "DOCFIND_FFMPEG does not run: "+err.Error())
	} else {
		line(true, "ffmpeg", strings.SplitN(string(out), "\n", 2)[0])
	}

	// The chat media folder, resolved as the server resolves it. Without
	// -users there is no state folder to resolve from, and the check must not
	// guess one (it once made chat-media in the working directory).
	if stateDir == "" || stateDir == "." {
		info("storage", "pass -users <accounts file> to check the chat media folder")
	} else if folder, err := chat.OpenMediaFolder(filepath.Join(stateDir, "chat-media"), filepath.Join(stateDir, "chat-media.json")); err != nil {
		line(false, "storage", err.Error())
	} else if root, from := folder.Root(); !isDir(root) {
		line(false, "storage", root+" ("+from+") does not exist yet; docfind makes it at start-up")
	} else if chat.CheckWritable(root) != nil {
		line(false, "storage", root+" ("+from+") cannot be written")
	} else {
		line(true, "storage", root+" ("+from+"), writable")
	}
	// TURN (calls): the running server must answer a STUN request.
	if cfg, on, err := calls.FromEnv(); err != nil {
		line(false, "turn", err.Error())
	} else if !on {
		info("turn", "DOCFIND_TURN_SECRET not set: calls connect only where the two browsers reach each other")
	} else if err := stunProbe(fmt.Sprintf("127.0.0.1:%d", cfg.Port)); err != nil {
		line(false, "turn", fmt.Sprintf("UDP %d does not answer STUN: %v", cfg.Port, err))
	} else {
		line(true, "turn", fmt.Sprintf("UDP %d answers STUN; relays on ports %d-%d", cfg.Port, cfg.MinPort, cfg.MaxPort))
	}

	// The group calls' media server of the running server (TCP and UDP 3479).
	if strings.EqualFold(strings.TrimSpace(os.Getenv("DOCFIND_SFU")), "off") {
		info("group calls", "off (DOCFIND_SFU=off)")
	} else {
		port := "3479"
		if p := strings.TrimSpace(os.Getenv("DOCFIND_SFU_PORT")); p != "" {
			port = p
		}
		if c, err := net.DialTimeout("tcp", "127.0.0.1:"+port, 3*time.Second); err != nil {
			line(false, "group calls", "the media server does not answer on TCP "+port+": "+err.Error())
		} else {
			c.Close()
			line(true, "group calls", "the media server answers on TCP "+port+" (and UDP)")
		}
	}

	// The socket endpoint of the running server: it must accept the upgrade
	// and then refuse a frame that is not a valid ticket.
	url := fmt.Sprintf("ws://127.0.0.1:%d/api/chat/ws", port)
	c, _, err := websocket.Dial(ctx, url, nil)
	if err != nil {
		line(false, "websocket", url+": "+err.Error())
	} else {
		b, _ := json.Marshal(map[string]any{"t": "auth", "d": map[string]string{"ticket": "not-a-ticket"}})
		c.Write(ctx, websocket.MessageText, b)
		_, _, err := c.Read(ctx)
		if code := websocket.CloseStatus(err); code == 4001 {
			line(true, "websocket", url+" answers and refuses a bad ticket")
		} else {
			line(false, "websocket", fmt.Sprintf("%s: unexpected answer to a bad ticket (%v)", url, err))
		}
	}
	if failed > 0 {
		return fmt.Errorf("%d check(s) failed", failed)
	}
	return nil
}

// migrateDown is -migrate-down N -confirm-data-loss. It runs one embedded down
// script; without the confirmation flag it only says what it would destroy.
func migrateDown(version int, confirmed bool) error {
	name, body, err := pgstore.DownScript(version)
	if err != nil {
		return err
	}
	if !confirmed {
		fmt.Printf("%s would run:\n%s\nNothing was changed. Add -confirm-data-loss to run it.\n", name, body)
		return errors.New("not confirmed")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	c := pgstore.FromEnv()
	if !c.Enabled() {
		return errors.New("DOCFIND_PG_* is not set")
	}
	db, err := pgstore.Open(ctx, c)
	if err != nil {
		return err
	}
	defer db.Close()
	ran, err := db.MigrateDown(ctx, version)
	if err == nil {
		fmt.Printf("ran %s\n", ran)
	}
	return err
}

func isDir(p string) bool {
	st, err := os.Stat(p)
	return err == nil && st.IsDir()
}

// stunProbe sends one STUN binding request to addr and waits for the answer.
func stunProbe(addr string) error {
	c, err := net.Dial("udp4", addr)
	if err != nil {
		return err
	}
	defer c.Close()
	req := stun.MustBuild(stun.TransactionID, stun.BindingRequest, stun.Fingerprint)
	if _, err := c.Write(req.Raw); err != nil {
		return err
	}
	c.SetReadDeadline(time.Now().Add(3 * time.Second))
	buf := make([]byte, 1500)
	n, err := c.Read(buf)
	if err != nil {
		return err
	}
	res := &stun.Message{Raw: buf[:n]}
	if err := res.Decode(); err != nil {
		return err
	}
	if res.Type != stun.BindingSuccess || res.TransactionID != req.TransactionID {
		return fmt.Errorf("unexpected answer %v", res.Type)
	}
	return nil
}
