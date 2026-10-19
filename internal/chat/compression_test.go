package chat

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"

	"github.com/Schayan-nk/docfind-messenger/internal/auth"
)

// The socket must never agree to permessage-deflate. IIS in front of docfind
// passes the extension header through and then drops the connection at the
// first compressed frame: 6.0.0 negotiated it, and no browser ever got past
// "connecting" on the public address.
func TestSocketNeverNegotiatesCompression(t *testing.T) {
	svc := New(nil, fakeUsers(nil), auth.NewSigner([]byte(strings.Repeat("k", 32)), time.Hour))
	srv := httptest.NewServer(http.HandlerFunc(svc.ServeWS))
	defer srv.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	c, resp, err := websocket.Dial(ctx, "ws"+strings.TrimPrefix(srv.URL, "http"), &websocket.DialOptions{CompressionMode: websocket.CompressionContextTakeover})
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer c.CloseNow()
	if ext := resp.Header.Get("Sec-WebSocket-Extensions"); ext != "" {
		t.Fatalf("the socket negotiated %q", ext)
	}
}
