package find

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/Schayan-nk/docfind-messenger/internal/auth"
	"github.com/Schayan-nk/docfind-messenger/internal/notify"
)

// The messenger's Android app (android/, from docfind's, 2026-10-19), from the
// server's side. GETTING IT: the signed APK sits in a folder (UseAndroidApp),
// named messenger-android-<version>-<code>.apk; the highest code is offered
// at /app/messenger.apk, to somebody signed in. NOTIFICATIONS: a WebView has
// no Push API, so with the app closed it asks every quarter of an hour
// (POST /api/app/poll) and shows what is new as the phone's own notification;
// with the app open the page's socket shows it at once (chatnotify.js).

type appDeviceStore interface {
	TouchAppDevice(ctx context.Context, device, username, label, ua string) (bool, error)
	NotificationsAfter(ctx context.Context, username string, after int64, limit int) ([]notify.Notification, error)
	LatestNotificationID(ctx context.Context, username string) (int64, error)
}

// appDeviceID is the random id the app makes once per installation.
var appDeviceID = regexp.MustCompile(`^[A-Za-z0-9-]{16,64}$`)

const appPollMax = 10

// handleAppPoll is POST /api/app/poll: {device, label, after} -> the caller's
// notifications newer than after, and the id to ask after next time. A device
// never seen, or just handed on, gets only where to count from: a phone that
// has just signed in must not ring for the backlog, nor show somebody else's.
func (s *Server) handleAppPoll(w http.ResponseWriter, r *http.Request) {
	if s.pushOff(w) {
		return
	}
	store, ok := s.push.store.(appDeviceStore)
	if !ok {
		writeJSONError(w, http.StatusNotFound, `{"error":"اعلان برنامهٔ اندروید روی این سرور نیست."}`)
		return
	}
	var req struct {
		Device string `json:"device"`
		Label  string `json:"label"`
		After  int64  `json:"after"`
		// Wait is how long to hold the request when nothing is new (app
		// 1.0.1 on, its LiveService): answered the moment something is.
		Wait int `json:"wait"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 2<<10)).Decode(&req); err != nil || !appDeviceID.MatchString(req.Device) {
		writeJSONError(w, http.StatusBadRequest, `{"error":"درخواست نامعتبر است."}`)
		return
	}
	who := auth.UserFrom(r).Username
	label := strings.TrimSpace(req.Label)
	if len([]rune(label)) > 60 {
		label = string([]rune(label)[:60])
	}
	fresh, err := store.TouchAppDevice(r.Context(), req.Device, who, label, r.UserAgent())
	if err != nil {
		logReportError("app poll: the device", err)
		writeJSONError(w, http.StatusBadGateway, `{"error":"اعلان‌ها خوانده نشد."}`)
		return
	}
	type item struct {
		ID       int64  `json:"id"`
		Title    string `json:"title"`
		Body     string `json:"body"`
		URL      string `json:"url"`
		Tag      string `json:"tag,omitempty"`
		Priority string `json:"priority"`
		Quiet    bool   `json:"quiet,omitempty"`
	}
	out := []item{}
	last := req.After
	if fresh || req.After <= 0 {
		if last, err = store.LatestNotificationID(r.Context(), who); err != nil {
			logReportError("app poll: the latest", err)
			writeJSONError(w, http.StatusBadGateway, `{"error":"اعلان‌ها خوانده نشد."}`)
			return
		}
	} else {
		list, err := store.NotificationsAfter(r.Context(), who, req.After, appPollMax)
		if err == nil && len(list) == 0 && req.Wait > 0 {
			// Held until something is new: the bell is listened to before
			// looking again, so one recorded in between is not missed.
			bell := s.android.bell.wait(who)
			if list, err = store.NotificationsAfter(r.Context(), who, req.After, appPollMax); err == nil && len(list) == 0 {
				select {
				case <-bell:
				case <-time.After(time.Duration(min(req.Wait, 50)) * time.Second):
				case <-r.Context().Done():
					return
				}
				list, err = store.NotificationsAfter(r.Context(), who, req.After, appPollMax)
			}
		}
		if err != nil {
			logReportError("app poll", err)
			writeJSONError(w, http.StatusBadGateway, `{"error":"اعلان‌ها خوانده نشد."}`)
			return
		}
		for _, n := range list {
			pri := ""
			if n.Urgency == "high" {
				pri = "important" // a private message, a mention, a ringing call: sound and heads-up
			}
			out = append(out, item{n.ID, n.Title, n.Body, appLocalURL(n.URL), n.Tag, pri, n.Quiet})
			if n.ID > last {
				last = n.ID
			}
		}
	}
	writeJSON(w, map[string]any{"notifications": out, "last": last})
}

// appLocalURL is where pressing a notification takes the app: a path here only.
func appLocalURL(u string) string {
	if !strings.HasPrefix(u, "/") || strings.HasPrefix(u, "//") {
		return "/"
	}
	return u
}

var apkName = regexp.MustCompile(`^messenger-android-(\d+\.\d+\.\d+)-(\d+)\.apk$`)

type apkInfo struct {
	path    string
	Version string `json:"version"`
	Code    int    `json:"code"`
	Size    int64  `json:"size"`
	SHA256  string `json:"sha256"`
}

// androidApp is the APK folder and the last APK found there, kept until the
// file changes.
type androidApp struct {
	dir  string
	mu   sync.Mutex
	key  string
	info *apkInfo
	// bell wakes the held polls of one person when a notification for
	// them is recorded (notifychat.go).
	bell appBell
}

// appBell is a channel per person, closed (and so every waiter woken) by ring.
type appBell struct {
	mu sync.Mutex
	ch map[string]chan struct{}
}

func (b *appBell) wait(user string) <-chan struct{} {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.ch == nil {
		b.ch = map[string]chan struct{}{}
	}
	c := b.ch[user]
	if c == nil {
		c = make(chan struct{})
		b.ch[user] = c
	}
	return c
}

func (b *appBell) ring(user string) {
	b.mu.Lock()
	c := b.ch[user]
	delete(b.ch, user)
	b.mu.Unlock()
	if c != nil {
		close(c)
	}
}

// UseAndroidApp offers the APK in dir.
func (s *Server) UseAndroidApp(dir string) { s.android.dir = dir }

func (a *androidApp) latestAPK() (*apkInfo, error) {
	if a.dir == "" {
		return nil, nil
	}
	entries, err := os.ReadDir(a.dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var best *apkInfo
	var bestFI os.FileInfo
	for _, e := range entries {
		m := apkName.FindStringSubmatch(e.Name())
		if m == nil || e.IsDir() {
			continue
		}
		code, err := strconv.Atoi(m[2])
		if err != nil {
			continue
		}
		fi, err := e.Info()
		if err != nil || !fi.Mode().IsRegular() {
			continue
		}
		if best == nil || code > best.Code {
			best = &apkInfo{path: filepath.Join(a.dir, e.Name()), Version: m[1], Code: code, Size: fi.Size()}
			bestFI = fi
		}
	}
	if best == nil {
		return nil, nil
	}
	key := fmt.Sprintf("%s|%d|%d", best.path, bestFI.Size(), bestFI.ModTime().UnixNano())
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.key == key && a.info != nil {
		return a.info, nil
	}
	f, err := os.Open(best.path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return nil, err
	}
	best.SHA256 = strings.ToUpper(hex.EncodeToString(h.Sum(nil)))
	a.key, a.info = key, best
	return best, nil
}

// handleAPKInfo is GET /api/app/android: the version on offer.
func (s *Server) handleAPKInfo(w http.ResponseWriter, r *http.Request) {
	info, err := s.android.latestAPK()
	if err != nil {
		logReportError("android app", err)
	}
	if info == nil {
		writeJSON(w, map[string]any{"available": false})
		return
	}
	writeJSON(w, map[string]any{"available": true, "version": info.Version, "code": info.Code,
		"size": info.Size, "sha256": info.SHA256})
}

// handleAPK is GET /app/messenger.apk: the file, named with its version.
func (s *Server) handleAPK(w http.ResponseWriter, r *http.Request) {
	info, err := s.android.latestAPK()
	if err != nil {
		logReportError("android app", err)
	}
	if info == nil {
		http.Error(w, "برنامهٔ اندروید هنوز روی سرور نیست.", http.StatusNotFound)
		return
	}
	f, err := os.Open(info.path)
	if err != nil {
		logReportError("android app", err)
		http.Error(w, "برنامه خوانده نشد.", http.StatusInternalServerError)
		return
	}
	defer f.Close()
	w.Header().Set("Content-Type", "application/vnd.android.package-archive")
	w.Header().Set("Content-Disposition", `attachment; filename="messenger-`+info.Version+`.apk"`)
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	http.ServeContent(w, r, "", time.Time{}, f)
}
