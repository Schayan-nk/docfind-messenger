package webauthn

import (
	"errors"
	"fmt"
	"math"
)

// A CBOR decoder (RFC 8949) for exactly what WebAuthn sends: the attestation
// object (a map of text keys) and the COSE public key (a map of integer
// keys). Unsigned and negative integers, byte and text strings, arrays, maps,
// and the simple values false/true/null. No tags, no floats, no
// indefinite lengths -- an authenticator that sent those would be sending
// something this application does not need, and it is refused rather than
// half-read.
//
// Written here rather than imported for the same reason as the QR encoder:
// it is small, it parses what strangers send, and every line is in view.

var errCBOR = errors.New("webauthn: malformed CBOR")

const (
	maxDepth = 8
	maxItems = 1024 // entries in any one array or map
)

// decodeCBOR reads one item from b and returns it with the bytes it used.
// Integers come back as int64, strings as string, byte strings as []byte,
// arrays as []any and maps as map[any]any.
func decodeCBOR(b []byte) (any, int, error) {
	return decodeItem(b, 0)
}

func decodeItem(b []byte, depth int) (any, int, error) {
	if depth > maxDepth || len(b) == 0 {
		return nil, 0, errCBOR
	}
	major, info := b[0]>>5, b[0]&0x1f
	arg, n, err := readArg(b, info)
	if err != nil {
		return nil, 0, err
	}
	switch major {
	case 0: // unsigned
		if arg > math.MaxInt64 {
			return nil, 0, errCBOR
		}
		return int64(arg), n, nil
	case 1: // negative: -1 - arg
		if arg > math.MaxInt64 {
			return nil, 0, errCBOR
		}
		return -1 - int64(arg), n, nil
	case 2, 3: // byte string, text string
		if arg > uint64(len(b)-n) {
			return nil, 0, errCBOR
		}
		end := n + int(arg)
		if major == 2 {
			return append([]byte(nil), b[n:end]...), end, nil
		}
		return string(b[n:end]), end, nil
	case 4: // array
		if arg > maxItems {
			return nil, 0, errCBOR
		}
		out := make([]any, 0, arg)
		for i := uint64(0); i < arg; i++ {
			v, used, err := decodeItem(b[n:], depth+1)
			if err != nil {
				return nil, 0, err
			}
			out = append(out, v)
			n += used
		}
		return out, n, nil
	case 5: // map
		if arg > maxItems {
			return nil, 0, errCBOR
		}
		out := make(map[any]any, arg)
		for i := uint64(0); i < arg; i++ {
			k, used, err := decodeItem(b[n:], depth+1)
			if err != nil {
				return nil, 0, err
			}
			n += used
			switch k.(type) {
			case int64, string:
			default:
				return nil, 0, errCBOR // only integer and text keys
			}
			if _, dup := out[k]; dup {
				return nil, 0, errCBOR
			}
			v, used, err := decodeItem(b[n:], depth+1)
			if err != nil {
				return nil, 0, err
			}
			n += used
			out[k] = v
		}
		return out, n, nil
	case 7: // simple values
		switch info {
		case 20:
			return false, 1, nil
		case 21:
			return true, 1, nil
		case 22:
			return nil, 1, nil
		}
	}
	return nil, 0, fmt.Errorf("%w: unsupported item 0x%02x", errCBOR, b[0])
}

// readArg reads the argument that follows the initial byte.
func readArg(b []byte, info byte) (uint64, int, error) {
	switch {
	case info < 24:
		return uint64(info), 1, nil
	case info >= 24 && info <= 27:
		size := 1 << (info - 24) // 1, 2, 4 or 8 bytes
		if len(b) < 1+size {
			return 0, 0, errCBOR
		}
		var v uint64
		for _, c := range b[1 : 1+size] {
			v = v<<8 | uint64(c)
		}
		return v, 1 + size, nil
	}
	return 0, 0, errCBOR // reserved, or indefinite length
}
