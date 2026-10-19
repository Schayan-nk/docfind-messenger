package qr

// The module grid: function patterns, data placement, masking and the
// penalty used to choose a mask. x is the column and y the row throughout.

type builder struct {
	ver      int
	size     int
	modules  [][]bool
	function [][]bool
}

func newBuilder(ver int) *builder {
	size := ver*4 + 17
	b := &builder{ver: ver, size: size}
	b.modules = make([][]bool, size)
	b.function = make([][]bool, size)
	for i := range b.modules {
		b.modules[i] = make([]bool, size)
		b.function[i] = make([]bool, size)
	}
	return b
}

func (b *builder) set(x, y int, dark bool) {
	b.modules[y][x] = dark
	b.function[y][x] = true
}

func (b *builder) drawFunctionPatterns() {
	for i := 0; i < b.size; i++ { // timing
		b.set(6, i, i%2 == 0)
		b.set(i, 6, i%2 == 0)
	}
	b.drawFinder(3, 3)
	b.drawFinder(b.size-4, 3)
	b.drawFinder(3, b.size-4)

	pos := alignments[b.ver]
	n := len(pos)
	for i := 0; i < n; i++ {
		for j := 0; j < n; j++ {
			// Not over the three finders.
			if (i == 0 && j == 0) || (i == 0 && j == n-1) || (i == n-1 && j == 0) {
				continue
			}
			b.drawAlignment(pos[i], pos[j])
		}
	}
	b.drawFormatBits(0) // reserves the area; redrawn with the real mask
	b.drawVersion()
}

// drawFinder draws a finder and its separator around centre (x, y).
func (b *builder) drawFinder(x, y int) {
	for dy := -4; dy <= 4; dy++ {
		for dx := -4; dx <= 4; dx++ {
			xx, yy := x+dx, y+dy
			if xx < 0 || xx >= b.size || yy < 0 || yy >= b.size {
				continue
			}
			d := max(abs(dx), abs(dy))
			b.set(xx, yy, d != 2 && d != 4)
		}
	}
}

func (b *builder) drawAlignment(x, y int) {
	for dy := -2; dy <= 2; dy++ {
		for dx := -2; dx <= 2; dx++ {
			b.set(x+dx, y+dy, max(abs(dx), abs(dy)) != 1)
		}
	}
}

// drawFormatBits writes level M and the mask, BCH-protected, in both copies.
func (b *builder) drawFormatBits(mask int) {
	data := 0<<3 | mask // level M is 00
	rem := data
	for i := 0; i < 10; i++ {
		rem = (rem << 1) ^ ((rem >> 9) * 0x537)
	}
	bits := (data<<10 | rem) ^ 0x5412
	bit := func(i int) bool { return (bits>>i)&1 == 1 }

	for i := 0; i <= 5; i++ {
		b.set(8, i, bit(i))
	}
	b.set(8, 7, bit(6))
	b.set(8, 8, bit(7))
	b.set(7, 8, bit(8))
	for i := 9; i < 15; i++ {
		b.set(14-i, 8, bit(i))
	}
	for i := 0; i < 8; i++ {
		b.set(b.size-1-i, 8, bit(i))
	}
	for i := 8; i < 15; i++ {
		b.set(8, b.size-15+i, bit(i))
	}
	b.set(8, b.size-8, true) // the dark module
}

// drawVersion writes the version number, BCH-protected, from version 7 up.
func (b *builder) drawVersion() {
	if b.ver < 7 {
		return
	}
	rem := b.ver
	for i := 0; i < 12; i++ {
		rem = (rem << 1) ^ ((rem >> 11) * 0x1F25)
	}
	bits := b.ver<<12 | rem
	for i := 0; i < 18; i++ {
		dark := (bits>>i)&1 == 1
		x, y := b.size-11+i%3, i/3
		b.set(x, y, dark)
		b.set(y, x, dark)
	}
}

// drawCodewords places the data in the zig-zag, two columns at a time from
// the right, skipping the vertical timing column.
func (b *builder) drawCodewords(data []byte) {
	i := 0
	for right := b.size - 1; right >= 1; right -= 2 {
		if right == 6 {
			right = 5
		}
		for vert := 0; vert < b.size; vert++ {
			for j := 0; j < 2; j++ {
				x := right - j
				upward := (right+1)&2 == 0
				y := vert
				if upward {
					y = b.size - 1 - vert
				}
				if !b.function[y][x] && i < len(data)*8 {
					b.modules[y][x] = (data[i>>3]>>(7-(i&7)))&1 == 1
					i++
				}
			}
		}
	}
}

// applyMask XORs a mask pattern over the data modules; applying it twice
// undoes it.
func (b *builder) applyMask(mask int) {
	for y := 0; y < b.size; y++ {
		for x := 0; x < b.size; x++ {
			var inv bool
			switch mask {
			case 0:
				inv = (x+y)%2 == 0
			case 1:
				inv = y%2 == 0
			case 2:
				inv = x%3 == 0
			case 3:
				inv = (x+y)%3 == 0
			case 4:
				inv = (x/3+y/2)%2 == 0
			case 5:
				inv = x*y%2+x*y%3 == 0
			case 6:
				inv = (x*y%2+x*y%3)%2 == 0
			case 7:
				inv = ((x+y)%2+x*y%3)%2 == 0
			}
			if inv && !b.function[y][x] {
				b.modules[y][x] = !b.modules[y][x]
			}
		}
	}
}

// penalty scores a masked grid; the mask with the lowest wins.
func (b *builder) penalty() int {
	const n1, n2, n3, n4 = 3, 3, 40, 10
	result := 0
	line := func(get func(i int) bool) {
		runColor, run := false, 0
		var hist [7]int
		for i := 0; i < b.size; i++ {
			if get(i) == runColor {
				run++
				if run == 5 {
					result += n1
				} else if run > 5 {
					result++
				}
			} else {
				b.addHistory(run, &hist)
				if !runColor {
					result += b.countFinderLike(&hist) * n3
				}
				runColor, run = get(i), 1
			}
		}
		result += b.terminateAndCount(runColor, run, &hist) * n3
	}
	for y := 0; y < b.size; y++ {
		line(func(x int) bool { return b.modules[y][x] })
	}
	for x := 0; x < b.size; x++ {
		line(func(y int) bool { return b.modules[y][x] })
	}
	for y := 0; y < b.size-1; y++ {
		for x := 0; x < b.size-1; x++ {
			c := b.modules[y][x]
			if c == b.modules[y][x+1] && c == b.modules[y+1][x] && c == b.modules[y+1][x+1] {
				result += n2
			}
		}
	}
	dark := 0
	for y := 0; y < b.size; y++ {
		for x := 0; x < b.size; x++ {
			if b.modules[y][x] {
				dark++
			}
		}
	}
	total := b.size * b.size
	k := (abs(dark*20-total*10)+total-1)/total - 1
	result += k * n4
	return result
}

func (b *builder) addHistory(run int, hist *[7]int) {
	if hist[0] == 0 {
		run += b.size // the light border before the first run
	}
	copy(hist[1:], hist[:6])
	hist[0] = run
}

func (b *builder) countFinderLike(hist *[7]int) int {
	n := hist[1]
	core := n > 0 && hist[2] == n && hist[3] == n*3 && hist[4] == n && hist[5] == n
	c := 0
	if core && hist[0] >= n*4 && hist[6] >= n {
		c++
	}
	if core && hist[6] >= n*4 && hist[0] >= n {
		c++
	}
	return c
}

func (b *builder) terminateAndCount(runColor bool, run int, hist *[7]int) int {
	if runColor {
		b.addHistory(run, hist)
		run = 0
	}
	run += b.size // the light border after the last run
	b.addHistory(run, hist)
	return b.countFinderLike(hist)
}

func abs(v int) int {
	if v < 0 {
		return -v
	}
	return v
}
