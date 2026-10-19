package main

import (
	"path/filepath"
	"testing"
)

func TestHTTPSIsOffUnlessBothHalvesAreSet(t *testing.T) {
	t.Setenv("DOCFIND_TLS_CERT", "")
	t.Setenv("DOCFIND_TLS_KEY", "")
	if cfg, err := serverTLS(); cfg != nil || err != nil {
		t.Errorf("with neither set: %v, %v", cfg, err)
	}
	// One without the other is a mistake, not plain HTTP.
	t.Setenv("DOCFIND_TLS_CERT", "cert.pem")
	if _, err := serverTLS(); err == nil {
		t.Error("a certificate without a key was accepted")
	}
}

// A server somebody believes is on HTTPS must not come up on HTTP instead.
func TestAnUnreadableCertificateStopsStartUp(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("DOCFIND_TLS_CERT", filepath.Join(dir, "missing.pem"))
	t.Setenv("DOCFIND_TLS_KEY", filepath.Join(dir, "missing.key"))
	if cfg, err := serverTLS(); err == nil || cfg != nil {
		t.Errorf("a missing certificate gave %v, %v", cfg, err)
	}
}
