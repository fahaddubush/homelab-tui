package main

import (
	"bufio"
	"math"
	"strconv"
	"strings"
	"unicode/utf8"
)

// ───────────────────────── colour ─────────────────────────

type col struct {
	r, g, b uint8
	set     bool
}

func rgb(r, g, b uint8) col { return col{r, g, b, true} }

func mix(a, b col, t float64) col {
	if t <= 0 {
		return a
	}
	if t >= 1 {
		return b
	}
	f := func(x, y uint8) uint8 { return uint8(float64(x) + (float64(y)-float64(x))*t) }
	return col{f(a.r, b.r), f(a.g, b.g), f(a.b, b.b), true}
}

func gradient(stops []col, t float64) col {
	if t <= 0 {
		return stops[0]
	}
	if t >= 1 {
		return stops[len(stops)-1]
	}
	p := t * float64(len(stops)-1)
	i := int(p)
	return mix(stops[i], stops[i+1], p-float64(i))
}

// cyclic gradient – wraps around so it can drift forever.
func cycle(stops []col, t float64) col {
	t = t - math.Floor(t)
	t = math.Floor(t*40) / 40 // coarse steps keep terminal traffic small
	return gradient(stops, t)
}

var (
	cBgA    = rgb(8, 7, 22)
	cBgB    = rgb(30, 16, 62)
	cBgC    = rgb(12, 24, 54)
	cGlass  = rgb(24, 22, 60)
	cLine   = rgb(60, 56, 124)
	cText   = rgb(228, 226, 252)
	cDim    = rgb(132, 128, 184)
	cFaint  = rgb(84, 80, 136)
	cViolet = rgb(152, 112, 255)
	cPink   = rgb(255, 128, 214)
	cCyan   = rgb(96, 222, 255)
	cMint   = rgb(128, 255, 206)
	cAmber  = rgb(255, 208, 120)
	cCoral  = rgb(255, 107, 139)
	cWhite  = rgb(255, 250, 255)

	aurora = []col{cViolet, cPink, cCyan, cViolet}
	cool   = []col{cViolet, cCyan}
	warm   = []col{cPink, cAmber}
	heat   = []col{cCyan, cMint, cAmber, cCoral}
)

// ───────────────────────── canvas ─────────────────────────

type style struct {
	fg, bg       col
	bold, italic bool
	glass        bool
}

type cell struct {
	ch           rune
	fg, bg       col
	bold, italic bool
	glass        bool
}

type canvas struct {
	w, h    int
	cells   []cell
	t       float64
	tint    col
	skyTint col
}

func newCanvas(w, h int) *canvas {
	return &canvas{w: w, h: h, cells: make([]cell, w*h)}
}

func (c *canvas) at(x, y int) *cell {
	if x < 0 || y < 0 || x >= c.w || y >= c.h {
		return nil
	}
	return &c.cells[y*c.w+x]
}

func (c *canvas) put(x, y int, ch rune, st style) {
	if p := c.at(x, y); p != nil {
		*p = cell{ch, st.fg, st.bg, st.bold, st.italic, st.glass}
	}
}

func rl(s string) int { return utf8.RuneCountInString(s) }

func fit(s string, n int) string {
	if n <= 0 {
		return ""
	}
	if rl(s) <= n {
		return s
	}
	r := []rune(s)
	if n == 1 {
		return "…"
	}
	return string(r[:n-1]) + "…"
}

func (c *canvas) text(x, y int, s string, st style) int {
	i := 0
	for _, r := range s {
		c.put(x+i, y, r, st)
		i++
	}
	return i
}

// text that keeps whatever background already sits underneath.
func (c *canvas) ink(x, y int, s string, fg col, bold bool) int {
	i := 0
	for _, r := range s {
		if p := c.at(x+i, y); p != nil {
			p.ch, p.fg, p.bold = r, fg, bold
		}
		i++
	}
	return i
}

func (c *canvas) gradText(x, y int, s string, stops []col, phase float64, bold bool) int {
	n := rl(s)
	i := 0
	for _, r := range s {
		t := float64(i)/float64(max(n, 1)) + phase
		if p := c.at(x+i, y); p != nil {
			p.ch, p.fg, p.bold = r, cycle(stops, t), bold
		}
		i++
	}
	return n
}

func (c *canvas) fill(x, y, w, h int, st style) {
	for j := 0; j < h; j++ {
		for i := 0; i < w; i++ {
			c.put(x+i, y+j, ' ', st)
		}
	}
}

func (c *canvas) panel(x, y, w, h int, title, right string, hot bool, phase float64) {
	if w < 6 || h < 3 {
		return
	}
	// Retain the sky in otherwise empty glass; text and borders are painted later.
	for j := 0; j < h; j++ {
		for i := 0; i < w; i++ {
			if p := c.at(x+i, y+j); p != nil {
				if p.ch != '·' && p.ch != '.' {
					*p = cell{ch: ' '}
				}
				p.glass = true
			}
		}
	}
	edge := func(i, n int) col {
		if c.tint.set {
			return c.tint
		}
		if hot {
			return cycle(aurora, float64(i)/float64(max(n, 1))*0.8+phase)
		}
		return cLine
	}
	for i := 1; i < w-1; i++ {
		c.put(x+i, y, '─', style{fg: edge(i, w), glass: true})
		c.put(x+i, y+h-1, '─', style{fg: edge(i, w), glass: true})
	}
	for j := 1; j < h-1; j++ {
		c.put(x, y+j, '│', style{fg: edge(0, w), glass: true})
		c.put(x+w-1, y+j, '│', style{fg: edge(w, w), glass: true})
	}
	c.put(x, y, '╭', style{fg: edge(0, w), glass: true})
	c.put(x+w-1, y, '╮', style{fg: edge(w, w), glass: true})
	c.put(x, y+h-1, '╰', style{fg: edge(0, w), glass: true})
	c.put(x+w-1, y+h-1, '╯', style{fg: edge(w, w), glass: true})
	if title != "" {
		t := fit(title, w-8)
		c.text(x+2, y, " "+t+" ", style{fg: cText, bold: true, glass: true})
	}
	if right != "" {
		r := fit(right, w-8-rl(title))
		c.text(x+w-3-rl(r), y, " "+r+" ", style{fg: cDim, glass: true})
	}
}

func (c *canvas) bar(x, y, w int, frac float64, stops []col, phase float64) {
	frac = math.Max(0, math.Min(1, frac))
	filled := frac * float64(w)
	for i := 0; i < w; i++ {
		switch {
		case float64(i+1) <= filled:
			c.ink(x+i, y, "━", cycle2(stops, float64(i)/float64(max(w-1, 1)), phase), false)
		case float64(i) < filled && filled-float64(i) >= 0.5:
			c.ink(x+i, y, "╸", cycle2(stops, float64(i)/float64(max(w-1, 1)), phase), false)
		default:
			c.ink(x+i, y, "━", cLine, false)
		}
	}
}

func cycle2(stops []col, t, phase float64) col {
	if phase == 0 {
		return gradient(stops, t)
	}
	return gradient(stops, t)
}

var blocks = []rune("▁▂▃▄▅▆▇█")

func tail(data []float64, n int) []float64 {
	if len(data) >= n {
		return data[len(data)-n:]
	}
	out := make([]float64, n)
	copy(out[n-len(data):], data)
	return out
}

func (c *canvas) spark(x, y, w int, data []float64, hi float64, stops []col) {
	d := tail(data, w)
	if hi <= 0 {
		hi = 1
	}
	for i, v := range d {
		f := math.Max(0, math.Min(1, v/hi))
		lvl := int(f*7 + 0.5)
		ch := blocks[lvl]
		if v <= 0 {
			ch = '▁'
		}
		colr := gradient(stops, f)
		if v <= 0 {
			colr = cLine
		}
		c.ink(x+i, y, string(ch), colr, false)
	}
}

// two-row spark, 16 levels of resolution
func (c *canvas) spark2(x, y, w int, data []float64, hi float64, stops []col) {
	d := tail(data, w)
	if hi <= 0 {
		hi = 1
	}
	for i, v := range d {
		f := math.Max(0, math.Min(1, v/hi))
		lv := int(f*16 + 0.5)
		low, top := lv, lv-8
		if low > 8 {
			low = 8
		}
		colr := gradient(stops, f)
		put := func(row, n int) {
			if n <= 0 {
				c.ink(x+i, row, "▁", cLine, false)
				return
			}
			c.ink(x+i, row, string(blocks[n-1]), colr, false)
		}
		put(y+1, low)
		if top > 0 {
			put(y, top)
		} else {
			c.ink(x+i, y, " ", cLine, false)
		}
	}
}

// ───────────────────────── backdrop ─────────────────────────

func hash2(x, y int) uint32 {
	h := uint32(x)*374761393 + uint32(y)*668265263
	h = (h ^ (h >> 13)) * 1274126177
	return h ^ (h >> 16)
}

func bgAt(x, y int, t float64) col {
	// Slow, quantized curtains keep a visible drift without flooding SSH.
	t = math.Floor(t*2) / 2
	v := 0.5 + 0.5*math.Sin(float64(x)*0.045+t*0.14+math.Sin(float64(y)*0.20+t*0.10)*1.4)
	u := 0.5 + 0.5*math.Sin(float64(x)*0.026-t*0.09+float64(y)*0.16)
	v = math.Floor(v*9) / 9
	u = math.Floor(u*8) / 8
	base := mix(cBgA, cBgB, v*0.82)
	return mix(base, cBgC, u*0.58)
}

func glassAt(x, y int, t float64) col {
	return mix(bgAt(x, y, t), cGlass, 0.53)
}

func (c *canvas) backdrop() {
	for y := 0; y < c.h; y++ {
		for x := 0; x < c.w; x++ {
			p := &c.cells[y*c.w+x]
			*p = cell{ch: ' '}
			if y <= 3 || y >= c.h-1 {
				continue
			}
			// Fixed-depth layers move at different speeds; stars stay small and soft.
			farX := x - int(math.Floor(c.t*0.035))
			nearX := x - int(math.Floor(c.t*0.11))
			far, near := hash2(farX+8192, y+201), hash2(nearX+8192, y+1091)
			if far%70 == 0 || near%130 == 0 {
				seed := far
				if near%130 == 0 {
					seed = near
				}
				phase := float64(seed%628) / 100
				b := math.Floor((0.5+0.5*math.Sin(c.t*(0.45+float64(seed%5)*0.16)+phase))*7) / 7
				p.ch = '.'
				strength := 0.20 + 0.38*b
				if near%130 == 0 {
					p.ch = '·'
					strength = 0.32 + 0.48*b
				}
				p.fg = mix(cBgB, cText, strength)
			}
		}
	}
}

// Three short trails cross only empty cells, beginning after each five idle seconds.
func (c *canvas) idleComets(idle float64) {
	if idle < 5 || c.w < 20 || c.h < 10 {
		return
	}
	phase := math.Mod(idle-5, 5)
	if phase >= 1.75 {
		return
	}
	cycleNo := int((idle - 5) / 5)
	for n := 0; n < 3; n++ {
		p := (phase - float64(n)*0.12) / 1.35
		if p < 0 || p > 1 {
			continue
		}
		row := 5 + int(hash2(cycleNo+n*19, n+41)%uint32(max(c.h-9, 1)))
		head := int(p*float64(c.w+12)) - 6
		for k := 0; k < 7; k++ {
			x := head - k
			if x < 1 || x >= c.w-1 {
				continue
			}
			cell := c.at(x, row)
			if cell == nil || (cell.ch != ' ' && cell.ch != '·') {
				continue
			}
			cell.ch = '·'
			if k == 0 {
				cell.ch = '·'
			}
			cell.fg = mix(cViolet, cWhite, 0.2+0.8*(1-float64(k)/7))
		}
	}
}

// resolve backgrounds and default foregrounds.
func (c *canvas) resolve() {
	for y := 0; y < c.h; y++ {
		for x := 0; x < c.w; x++ {
			p := &c.cells[y*c.w+x]
			if !p.bg.set {
				if p.glass {
					p.bg = glassAt(x, y, c.t)
				} else {
					p.bg = bgAt(x, y, c.t)
				}
				if c.skyTint.set {
					p.bg = mix(p.bg, c.skyTint, 0.13)
				}
			}
			if !p.fg.set {
				p.fg = cText
			}
		}
	}
}

// dim everything inside a rectangle (used behind modals)
func (c *canvas) dim(x0, y0, w, h int, amount float64) {
	for y := y0; y < y0+h; y++ {
		for x := x0; x < x0+w; x++ {
			if p := c.at(x, y); p != nil {
				p.fg = mix(p.fg, cBgA, amount)
				p.bg = mix(p.bg, cBgA, amount*0.8)
			}
		}
	}
}

// ───────────────────────── terminal output ─────────────────────────

type screen struct {
	w, h int
	prev []cell
	out  *bufio.Writer
}

func sameCell(a, b cell) bool {
	return a.ch == b.ch && a.fg == b.fg && a.bg == b.bg && a.bold == b.bold && a.italic == b.italic
}

func (s *screen) invalidate() { s.prev = nil }

func (s *screen) flush(c *canvas) {
	full := s.prev == nil || len(s.prev) != len(c.cells)
	w := s.out
	if full {
		w.WriteString("\x1b[0m\x1b[2J")
	}
	var cf, cb col
	var cbold, cital bool
	started := false
	same := func(i int) bool { return !full && sameCell(s.prev[i], c.cells[i]) }
	emit := func(p cell) {
		if !started || p.bold != cbold {
			if p.bold {
				w.WriteString("\x1b[1m")
			} else {
				w.WriteString("\x1b[22m")
			}
			cbold = p.bold
		}
		if !started || p.italic != cital {
			if p.italic {
				w.WriteString("\x1b[3m")
			} else {
				w.WriteString("\x1b[23m")
			}
			cital = p.italic
		}
		if !started || p.fg != cf {
			w.WriteString("\x1b[38;2;" + strconv.Itoa(int(p.fg.r)) + ";" + strconv.Itoa(int(p.fg.g)) + ";" + strconv.Itoa(int(p.fg.b)) + "m")
			cf = p.fg
		}
		if !started || p.bg != cb {
			w.WriteString("\x1b[48;2;" + strconv.Itoa(int(p.bg.r)) + ";" + strconv.Itoa(int(p.bg.g)) + ";" + strconv.Itoa(int(p.bg.b)) + "m")
			cb = p.bg
		}
		started = true
		w.WriteRune(p.ch)
	}
	for y := 0; y < c.h; y++ {
		x := 0
		for x < c.w {
			if same(y*c.w + x) {
				x++
				continue
			}
			w.WriteString("\x1b[" + strconv.Itoa(y+1) + ";" + strconv.Itoa(x+1) + "H")
			for x < c.w {
				emit(c.cells[y*c.w+x])
				x++
				gap := 0
				for x+gap < c.w && same(y*c.w+x+gap) {
					gap++
				}
				if x >= c.w || gap >= 4 || x+gap >= c.w {
					break
				}
			}
		}
	}
	w.WriteString("\x1b[0m")
	w.Flush()
	s.prev = append(s.prev[:0], c.cells...)
}

func joinLines(ss ...string) string { return strings.Join(ss, "\n") }
