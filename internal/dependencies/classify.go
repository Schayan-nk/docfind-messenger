package dependencies

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"net"
	"strings"
	"syscall"
)

// Classify names the kind of failure, which is what decides what to do
// about it: a timeout is the network or a load, "refused" is a service that
// is not running, "dns" and "tls" are configuration, "auth" is a password.
func Classify(err error) string {
	if err == nil {
		return ""
	}
	var (
		dnsErr  *net.DNSError
		netErr  net.Error
		certErr *tls.CertificateVerificationError
		unknown x509.UnknownAuthorityError
		invalid x509.CertificateInvalidError
		host    x509.HostnameError
	)
	msg := strings.ToLower(err.Error())
	switch {
	case errors.Is(err, ErrOpen):
		return "circuit_open"
	case errors.Is(err, context.DeadlineExceeded):
		return "timeout"
	case errors.As(err, &dnsErr):
		return "dns"
	case errors.As(err, &certErr), errors.As(err, &unknown), errors.As(err, &invalid), errors.As(err, &host),
		strings.Contains(msg, "tls:"), strings.Contains(msg, "x509:"):
		return "tls"
	case errors.Is(err, syscall.ECONNREFUSED), strings.Contains(msg, "refused"):
		return "refused"
	case errors.Is(err, syscall.ECONNRESET), strings.Contains(msg, "forcibly closed"),
		strings.Contains(msg, "connection reset"):
		return "reset"
	case strings.Contains(msg, "password authentication failed"), strings.Contains(msg, "access denied"),
		strings.Contains(msg, "login failed"), strings.Contains(msg, "authentication"),
		strings.Contains(msg, "401"), strings.Contains(msg, "403"):
		return "auth"
	case errors.As(err, &netErr) && netErr.Timeout():
		return "timeout"
	case errors.Is(err, net.ErrClosed), strings.Contains(msg, "unreachable"):
		return "unreachable"
	}
	return "other"
}
