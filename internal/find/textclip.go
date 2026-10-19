package find

// clipRunes cuts to a rune count, never a byte count.
//
// Every string here is Persian, where a byte cut lands mid-character and the
// tail arrives as replacement characters -- which reads as corrupted data
// rather than as a truncated field.
func clipRunes(s string, max int) string {
	r := []rune(s)
	if len(r) <= max {
		return s
	}
	return string(r[:max])
}
