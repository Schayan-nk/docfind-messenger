package qr

import (
	"errors"
	"strings"
	"testing"
)

func TestTooLongIsRefused(t *testing.T) {
	if _, err := Encode(strings.Repeat("a", 214)); !errors.Is(err, ErrTooLong) {
		t.Errorf("214 bytes: err = %v, want ErrTooLong", err)
	}
	if _, err := Encode(strings.Repeat("a", 213)); err != nil {
		t.Errorf("213 bytes, the most version 10 holds: %v", err)
	}
}

func TestTheSVGIsOnlyAPicture(t *testing.T) {
	c, err := Encode("otpauth://totp/docfind:ali?secret=JBSWY3DPEHPK3PXP")
	if err != nil {
		t.Fatal(err)
	}
	svg := c.SVG()
	if !strings.HasPrefix(svg, "<svg ") || !strings.HasSuffix(svg, "</svg>") {
		t.Errorf("not an SVG document: %.60s...", svg)
	}
	// Modules and nothing else: no script, no link, no handler, and not the
	// secret itself in readable form.
	for _, bad := range []string{"<script", "href", " on", "otpauth", "JBSWY3DP"} {
		if strings.Contains(svg, bad) {
			t.Errorf("the SVG carries %q", bad)
		}
	}
}
