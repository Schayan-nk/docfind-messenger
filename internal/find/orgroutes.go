package find

// imageType identifies an image by its magic bytes, returning "" for anything
// it does not recognise.
func imageType(b []byte) string {
	switch {
	case len(b) > 3 && b[0] == 0xFF && b[1] == 0xD8 && b[2] == 0xFF:
		return "image/jpeg"
	case len(b) > 8 && string(b[:8]) == "\x89PNG\r\n\x1a\n":
		return "image/png"
	case len(b) > 12 && string(b[:4]) == "RIFF" && string(b[8:12]) == "WEBP":
		return "image/webp"
	case len(b) > 6 && (string(b[:6]) == "GIF87a" || string(b[:6]) == "GIF89a"):
		return "image/gif"
	case len(b) > 2 && b[0] == 0x42 && b[1] == 0x4D:
		return "image/bmp"
	}
	return ""
}
