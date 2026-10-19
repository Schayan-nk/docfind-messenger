package find

import "strings"

// faDigits writes the ASCII digits in s as Persian ones, for numbers in
// Persian copy.
func faDigits(s string) string {
	return strings.Map(func(r rune) rune {
		if r >= '0' && r <= '9' {
			return '\u06f0' + (r - '0')
		}
		return r
	}, s)
}
