package chat

import (
	"crypto/sha256"
	"encoding/hex"
	"strconv"
)

// derivedID is the client id of part i (i >= 1) of a message that was split.
// It is a function of the original id, so resending the whole long message
// after a lost answer finds every part it already made, not only the first.
func derivedID(cid string, i int) string {
	h := sha256.Sum256([]byte(cid + "#" + strconv.Itoa(i)))
	h[6] = (h[6] & 0x0f) | 0x80 // version 8: "custom" UUID
	h[8] = (h[8] & 0x3f) | 0x80 // RFC 4122 variant
	x := hex.EncodeToString(h[:16])
	return x[0:8] + "-" + x[8:12] + "-" + x[12:16] + "-" + x[16:20] + "-" + x[20:32]
}

// fmtSscan parses a chat id that arrived as a JSON object key.
func fmtSscan(s string, out *int64) (int, error) {
	v, err := strconv.ParseInt(s, 10, 64)
	if err != nil || v <= 0 {
		return 0, errNotNumber
	}
	*out = v
	return 1, nil
}
