package obs

import (
	"bytes"
	"io"
	"os"
	"strings"
	"sync"
)

// CaptureStdio routes everything the process prints into the log.
//
// WHY NOT CHANGE EACH CALL. There are over a hundred fmt.Printf calls across
// the server -- report failures, mail errors, sync results -- written when
// stdout was assumed to be read by somebody. Rewriting each one to call the
// logger is a hundred edits and a hundred chances to drop one; swapping the
// file they all write to is one, and it also catches the ones added next
// month by somebody who has not read this.
//
// Each line is written to the log as kind "stdout" or "stderr" and passed
// through to the original stream unchanged, so the console build and anybody
// running docfind by hand still see what they always saw.
//
// Returns a function that restores the original streams and waits for the
// last line to be written.
func CaptureStdio(l *Logger) (restore func()) {
	if l == nil || l.out == nil {
		return func() {}
	}
	origOut, origErr := os.Stdout, os.Stderr

	var wg sync.WaitGroup
	pipe := func(orig *os.File, kind, level string) (*os.File, bool) {
		r, w, err := os.Pipe()
		if err != nil {
			return nil, false
		}
		wg.Go(func() {
			lw := &lineWriter{emit: func(line string) {
				l.Write(kind, level, Fields{"msg": line})
			}}
			// The original stream may not exist at all -- the windowed build
			// has no console -- so a failed write there must not stop the
			// copy into the log.
			buf := make([]byte, 32<<10)
			for {
				n, rerr := r.Read(buf)
				if n > 0 {
					if orig != nil {
						_, _ = orig.Write(buf[:n])
					}
					_, _ = lw.Write(buf[:n])
				}
				if rerr != nil {
					lw.Flush()
					r.Close()
					return
				}
			}
		})
		return w, true
	}

	outW, okOut := pipe(origOut, "stdout", "info")
	errW, okErr := pipe(origErr, "stderr", "warn")
	if okOut {
		os.Stdout = outW
	}
	if okErr {
		os.Stderr = errW
	}
	return func() {
		if okOut {
			os.Stdout = origOut
			outW.Close()
		}
		if okErr {
			os.Stderr = origErr
			errW.Close()
		}
		wg.Wait()
	}
}

// lineWriter turns a byte stream into whole lines.
//
// A Printf of a multi-line message arrives in one Write; a large one can
// arrive in several. Emitting per Write would split a line across two log
// entries or merge two lines into one, and either makes the log wrong in the
// exact case -- a stack trace -- where it matters most. So bytes are held
// until a newline, and a panic's stack is kept as consecutive lines.
type lineWriter struct {
	buf  bytes.Buffer
	emit func(string)
}

// maxLine bounds how much is held waiting for a newline, so a writer that
// never sends one cannot grow the buffer without limit.
const maxLine = 64 << 10

func (lw *lineWriter) Write(p []byte) (int, error) {
	lw.buf.Write(p)
	for {
		i := bytes.IndexByte(lw.buf.Bytes(), '\n')
		if i < 0 {
			if lw.buf.Len() > maxLine {
				lw.emit(strings.TrimRight(lw.buf.String(), "\r"))
				lw.buf.Reset()
			}
			return len(p), nil
		}
		line := string(lw.buf.Next(i + 1))
		line = strings.TrimRight(line, "\r\n")
		if strings.TrimSpace(line) != "" {
			lw.emit(line)
		}
	}
}

// Flush emits whatever is left without a trailing newline.
func (lw *lineWriter) Flush() {
	if s := strings.TrimRight(lw.buf.String(), "\r\n"); strings.TrimSpace(s) != "" {
		lw.emit(s)
	}
	lw.buf.Reset()
}

var _ io.Writer = (*lineWriter)(nil)
