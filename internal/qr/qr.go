// Package qr draws a QR code, for one purpose: putting an authenticator
// app's setup address (otpauth://...) on a screen so a phone can read it.
//
// Written here rather than imported. It is a few hundred lines of published
// arithmetic (ISO/IEC 18004), it runs on a secret, and the alternative was a
// dependency to audit or a script from a CDN the page's CSP would have to
// admit -- for a secret that must not leave the server any other way.
//
// Deliberately narrow: byte mode, error-correction level M, versions 1 to 10
// (up to 213 bytes). An otpauth address is about a hundred. Anything longer
// is refused rather than drawn wrong.
//
// The construction follows Project Nayuki's reference implementation step
// for step -- the same tables, placement order and penalty rules -- so it can
// be checked against that line by line.
package qr

import (
	"errors"
	"fmt"
	"strings"
)

// Per version 1..10 at level M: codewords in the symbol, error-correction
// blocks, and error-correction codewords per block. Index 0 is unused.
var (
	rawCodewords = [11]int{0, 26, 44, 70, 100, 134, 172, 196, 242, 292, 346}
	numBlocks    = [11]int{0, 1, 1, 1, 2, 2, 4, 4, 4, 5, 5}
	eccPerBlock  = [11]int{0, 10, 16, 26, 18, 24, 16, 18, 22, 22, 26}
	alignments   = [11][]int{nil, nil, {6, 18}, {6, 22}, {6, 26}, {6, 30}, {6, 34},
		{6, 22, 38}, {6, 24, 42}, {6, 26, 46}, {6, 28, 50}}
)

// ErrTooLong is returned for text beyond version 10 at level M.
var ErrTooLong = errors.New("qr: text too long")

// Code is a drawn symbol: Size modules square, true for dark.
type Code struct {
	Size    int
	modules [][]bool
}

// Encode draws text as the smallest symbol that holds it.
func Encode(text string) (*Code, error) { return encode(text, -1) }

// encode is Encode with the mask chosen by the caller (0-7), or by penalty
// when mask is negative -- the tests compare each mask against a reference.
func encode(text string, mask int) (*Code, error) {
	data := []byte(text)
	ver := 0
	for v := 1; v <= 10; v++ {
		ccBits := 8
		if v >= 10 {
			ccBits = 16
		}
		if 4+ccBits+8*len(data) <= dataCodewords(v)*8 {
			ver = v
			break
		}
	}
	if ver == 0 {
		return nil, fmt.Errorf("%w: %d bytes", ErrTooLong, len(data))
	}

	words := encodeData(data, ver)
	all := interleave(words, ver)

	b := newBuilder(ver)
	b.drawFunctionPatterns()
	b.drawCodewords(all)

	best, bestPenalty := mask, 0
	for m := 0; m < 8 && mask < 0; m++ {
		b.applyMask(m)
		b.drawFormatBits(m)
		if p := b.penalty(); best < 0 || p < bestPenalty {
			best, bestPenalty = m, p
		}
		b.applyMask(m) // XOR again undoes it
	}
	b.applyMask(best)
	b.drawFormatBits(best)
	return &Code{Size: b.size, modules: b.modules}, nil
}

func dataCodewords(ver int) int { return rawCodewords[ver] - numBlocks[ver]*eccPerBlock[ver] }

// encodeData is the bit stream: mode, count, bytes, terminator and padding.
func encodeData(data []byte, ver int) []byte {
	var bits []bool
	put := func(val, n int) {
		for i := n - 1; i >= 0; i-- {
			bits = append(bits, (val>>i)&1 == 1)
		}
	}
	put(0x4, 4) // byte mode
	if ver >= 10 {
		put(len(data), 16)
	} else {
		put(len(data), 8)
	}
	for _, c := range data {
		put(int(c), 8)
	}
	capBits := dataCodewords(ver) * 8
	term := capBits - len(bits)
	if term > 4 {
		term = 4
	}
	put(0, term)
	for len(bits)%8 != 0 {
		bits = append(bits, false)
	}
	out := make([]byte, 0, dataCodewords(ver))
	for i := 0; i < len(bits); i += 8 {
		var v byte
		for j := 0; j < 8; j++ {
			if bits[i+j] {
				v |= 1 << (7 - j)
			}
		}
		out = append(out, v)
	}
	for pad := byte(0xEC); len(out) < dataCodewords(ver); pad ^= 0xEC ^ 0x11 {
		out = append(out, pad)
	}
	return out
}

// interleave splits the data into blocks, adds each block's error
// correction, and interleaves the lot.
func interleave(data []byte, ver int) []byte {
	nb, ecc, raw := numBlocks[ver], eccPerBlock[ver], rawCodewords[ver]
	numShort := nb - raw%nb
	shortLen := raw / nb
	div := rsDivisor(ecc)
	var blocks [][]byte
	k := 0
	for i := 0; i < nb; i++ {
		n := shortLen - ecc
		if i >= numShort {
			n++
		}
		dat := append([]byte(nil), data[k:k+n]...)
		k += n
		rem := rsRemainder(dat, div)
		if i < numShort {
			dat = append(dat, 0) // placeholder, skipped below
		}
		blocks = append(blocks, append(dat, rem...))
	}
	var out []byte
	for i := range blocks[0] {
		for j, blk := range blocks {
			if i != shortLen-ecc || j >= numShort {
				out = append(out, blk[i])
			}
		}
	}
	return out
}

// Reed-Solomon over GF(2^8) with the QR polynomial 0x11D.

func gfMul(x, y byte) byte {
	var z int
	for i := 7; i >= 0; i-- {
		z = (z << 1) ^ ((z >> 7) * 0x11D)
		z ^= int((y>>i)&1) * int(x)
	}
	return byte(z)
}

func rsDivisor(degree int) []byte {
	res := make([]byte, degree)
	res[degree-1] = 1
	root := byte(1)
	for i := 0; i < degree; i++ {
		for j := range res {
			res[j] = gfMul(res[j], root)
			if j+1 < len(res) {
				res[j] ^= res[j+1]
			}
		}
		root = gfMul(root, 0x02)
	}
	return res
}

func rsRemainder(data, div []byte) []byte {
	res := make([]byte, len(div))
	for _, b := range data {
		factor := b ^ res[0]
		copy(res, res[1:])
		res[len(res)-1] = 0
		for i := range res {
			res[i] ^= gfMul(div[i], factor)
		}
	}
	return res
}

// SVG draws the code with a four-module quiet zone, black on white whatever
// the page's theme -- a phone camera needs the contrast, not the palette.
func (c *Code) SVG() string {
	n := c.Size + 8
	var b strings.Builder
	fmt.Fprintf(&b, `<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 %d %d" shape-rendering="crispEdges">`, n, n)
	fmt.Fprintf(&b, `<rect width="%d" height="%d" fill="#fff"/><path fill="#000" d="`, n, n)
	for y := 0; y < c.Size; y++ {
		for x := 0; x < c.Size; x++ {
			if c.modules[y][x] {
				fmt.Fprintf(&b, "M%d %dh1v1h-1z", x+4, y+4)
			}
		}
	}
	b.WriteString(`"/></svg>`)
	return b.String()
}
