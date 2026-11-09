package main

import (
	"fmt"
	"math"
	"regexp"
	"strings"
	"time"
)

const version = "0.1.0"

type hit struct {
	x, y, w, h int
	kind       string // tab | item | out
	idx        int
}

type Modal struct {
	Title   string
	Body    []string
	Items   []string
	Idx     int
	Danger  bool
	Help    bool
	OnYes   func()
	OnPick  func(int)
	Opened  time.Time
	Confirm bool
}

type listItem struct {
	title, sub, tag string
	tagCol, barCol  col
}

func clamp(v, lo, hi float64) float64 { return math.Max(lo, math.Min(hi, v)) }
func ease(t float64) float64          { t = clamp(t, 0, 1); return 1 - math.Pow(1-t, 3) }

func (a *App) smooth(name string, target, rate float64) float64 {
	v, ok := a.disp[name]
	if !ok {
		v = target
	}
	v += (target - v) * rate
	a.disp[name] = v
	return v
}

func heatCol(p float64) col { return gradient(heat, clamp(p/100, 0, 1)) }

func wrapText(s string, w int) []string {
	var out []string
	for _, para := range strings.Split(s, "\n") {
		words := strings.Fields(para)
		line := ""
		for _, wd := range words {
			if line == "" {
				line = wd
			} else if rl(line)+1+rl(wd) <= w {
				line += " " + wd
			} else {
				out = append(out, line)
				line = wd
			}
		}
		out = append(out, line)
	}
	return out
}

// ───────────────────────── frame ─────────────────────────

func (a *App) render(c *canvas) {
	a.hits = a.hits[:0]
	colors := []col{rgb(46, 35, 120), rgb(35, 87, 120), rgb(105, 50, 114), rgb(68, 48, 100), rgb(26, 95, 116), rgb(81, 52, 125), rgb(65, 65, 112), rgb(43, 80, 120), rgb(102, 64, 85), rgb(64, 54, 124)}
	from, to := colors[a.previousTab%len(colors)], colors[a.tab%len(colors)]
	p := 1.0
	if a.cfg.Animations {
		p = ease(float64(a.now.Sub(a.tabStart)) / float64(900*time.Millisecond))
	}
	c.skyTint = mix(from, to, p)
	c.backdrop()
	if c.w < 96 || c.h < 28 {
		msg := fmt.Sprintf("homelab needs a larger window  ·  now %d×%d  ·  need 96×28", c.w, c.h)
		c.gradText(max(0, (c.w-rl(msg))/2), c.h/2, msg, aurora, a.t()*0.2, true)
		c.resolve()
		return
	}
	a.drawHeader(c)
	cx, cy, cw, ch := 2, 5, c.w-4, c.h-7
	switch tabs[a.tab].Kind {
	case kOverview:
		a.drawOverview(c, cx, cy, cw, ch)
	case kContainers:
		a.drawContainers(c, cx, cy, cw, ch)
	case kOps:
		a.drawOps(c, cx, cy, cw, ch)
	case kSettings:
		a.drawSettings(c, cx, cy, cw, ch)
	}
	// page transition: soft cascade reveal
	if p := float64(a.now.Sub(a.tabStart)) / float64(380*time.Millisecond); p < 1.6 && a.cfg.Animations {
		c.resolve()
		for y := cy; y < cy+ch; y++ {
			for x := cx - 1; x < cx+cw+1; x++ {
				if cl := c.at(x, y); cl != nil {
					k := ease(p*1.5 - (float64(x-cx)/float64(cw)*0.55 + float64(y-cy)/float64(ch)*0.35))
					cl.fg = mix(cl.bg, cl.fg, k)
				}
			}
		}
	}
	a.drawFooter(c)
	if a.modal == nil {
		a.drawCosmicExtras(c)
	}
	a.drawAmbientLight(c)
	c.resolve()
	if a.modal != nil {
		a.drawModal(c)
	}
	a.drawToast(c)
}

func (a *App) drawHeader(c *canvas) {
	t := a.t()
	// ribbon with a drifting comet
	pos := math.Mod(t*22, float64(c.w+30)) - 10
	for x := 0; x < c.w; x++ {
		base := mix(cycle(aurora, float64(x)/float64(c.w)*1.2+t*0.05), cBgA, 0.72)
		d := pos - float64(x)
		ch, fg := "─", base
		if d >= 0 && d < 14 {
			k := 1 - d/14
			ch, fg = "━", mix(base, cWhite, k)
		}
		c.ink(x, 0, ch, fg, false)
	}
	// wordmark
	title := "h o m e l a b"
	c.gradText(3, 1, title, aurora, t*0.12, true)
	if a.cfg.Animations { // shimmer
		sp := math.Mod(t*0.45, 2.2) - 0.6
		for i, r := range title {
			if r == ' ' {
				continue
			}
			k := 1 - math.Abs(float64(i)/float64(rl(title))-sp)*7
			if k > 0 {
				if p := c.at(3+i, 1); p != nil {
					p.fg = mix(p.fg, cWhite, k*0.85)
				}
			}
		}
	}
	a.mu.Lock()
	tel := a.tel
	a.mu.Unlock()
	right := fmt.Sprintf("%s   %s   up %s   %s", tel.Hostname, tel.IP, humanUp(tel.Uptime), a.now.Format("15:04:05"))
	if a.demo {
		right = "DEMO · fictional data"
	}
	c.ink(c.w-3-rl(right), 1, right, cDim, false)

	// tabs
	labels := make([]string, len(tabs))
	total := 0
	for i, tb := range tabs {
		labels[i] = tb.Name
		total += rl(tb.Name) + 3
	}
	compact := total > c.w-4
	if compact {
		total = 0
		for i, tb := range tabs {
			if i != a.tab {
				labels[i] = fit(tb.Name, 4)
			}
			total += rl(labels[i]) + 3
		}
	}
	x := 2
	var tx, tw float64
	for i := range tabs {
		w := rl(labels[i]) + 3
		a.hits = append(a.hits, hit{x, 2, w, 1, "tab", i})
		if i == a.tab {
			tx, tw = float64(x), float64(w-1)
			c.gradText(x+1, 2, labels[i], aurora, t*0.1, true)
		} else {
			c.ink(x+1, 2, labels[i], cDim, false)
		}
		x += w
	}
	a.tabX = a.tabX + (tx-a.tabX)*0.28
	a.tabW = a.tabW + (tw-a.tabW)*0.28
	if !a.cfg.Animations {
		a.tabX, a.tabW = tx, tw
	}
	for i := 1; i < c.w-1; i++ {
		c.ink(i, 3, "─", mix(cLine, cBgA, 0.35), false)
	}
	for i := 0; i < int(a.tabW+0.5); i++ {
		xx := int(a.tabX+0.5) + i
		c.ink(xx, 3, "━", cycle(aurora, float64(i)/math.Max(a.tabW, 1)+t*0.15), false)
	}
}

func (a *App) drawFooter(c *canvas) {
	y := c.h - 1
	type kv struct{ k, v string }
	hints := []kv{{"←→", "tabs"}, {"↑↓", "select"}, {"↵", "run"}, {"PgUp/Dn", "scroll"}, {"x", "stop"}, {"r", "refresh"}, {"?", "help"}, {"q", "quit"}}
	x := 3
	for _, h := range hints {
		x += c.ink(x, y, h.k, cViolet, true) + 1
		x += c.ink(x, y, h.v, cFaint, false) + 3
	}
}

// Light can wash behind text and panel edges, while preserving every glyph.
func (a *App) drawAmbientLight(c *canvas) {
	t := math.Floor(a.t()*2) / 2
	for y := 4; y < c.h-1; y++ {
		for x := 1; x < c.w-1; x++ {
			p := c.at(x, y)
			if p == nil || p.bg.set {
				continue
			}
			p.bg = a.ambientBackground(c, x, y, t, p.glass)
		}
	}
}

func (a *App) ambientBackground(c *canvas, x, y int, t float64, glass bool) col {
	t = math.Floor(t*2) / 2
	wave := 0.5 + 0.5*math.Sin(float64(x)*0.035+float64(y)*0.19-t*0.22+float64(a.tab)*0.8)
	strength := math.Floor(wave*5) / 5 * 0.14
	base := bgAt(x, y, t)
	if glass {
		base = glassAt(x, y, t)
	}
	return mix(base, c.skyTint, strength+0.06)
}

func (a *App) drawLabeledScene(c *canvas, x, y, w, h int) {
	if h >= 6 {
		c.ink(x, y, tabSceneName(a.tab), cDim, false)
		a.drawTabScene(c, x, y+1, w, h-1)
		return
	}
	a.drawTabScene(c, x, y, w, h)
}

func (a *App) drawToast(c *canvas) {
	a.tmu.Lock()
	msg, bad, at := a.toastMsg, a.toastBad, a.toastAt
	a.tmu.Unlock()
	if msg == "" {
		return
	}
	el := a.now.Sub(at).Seconds()
	if el > 3.6 {
		return
	}
	slide := int((1 - ease(el/0.3)) * 18)
	fade := clamp((3.6-el)/0.5, 0, 1)
	s := " " + msg + " "
	w := rl(s) + 2
	x := c.w - w - 3 + slide
	y := c.h - 3
	edge := cViolet
	if bad {
		edge = cCoral
	}
	bg := mix(cBgA, cGlass, 0.9)
	for i := 0; i < w; i++ {
		for j := 0; j < 3; j++ {
			if p := c.at(x+i, y-1+j); p != nil {
				*p = cell{ch: ' ', bg: bg, fg: cText}
			}
		}
	}
	c.put(x, y, '▌', style{fg: mix(bg, edge, fade), bg: bg})
	for i, r := range s {
		c.put(x+1+i, y, r, style{fg: mix(bg, cText, fade), bg: bg})
	}
}

// ───────────────────────── overview ─────────────────────────

func (a *App) drawOverview(c *canvas, x, y, w, h int) {
	t := a.t()
	a.mu.Lock()
	tel := a.tel
	tel.CPUHist = append([]float64(nil), a.tel.CPUHist...)
	tel.MemHist = append([]float64(nil), a.tel.MemHist...)
	tel.RxHist = append([]float64(nil), a.tel.RxHist...)
	tel.TxHist = append([]float64(nil), a.tel.TxHist...)
	a.mu.Unlock()

	topH := 7
	w1 := (w - 2) / 3
	w3 := w - 2*w1 - 2

	// CPU
	cpu := a.smooth("cpu", tel.CPU, 0.2)
	c.panel(x, y, w1, topH, "Processor", fmt.Sprintf("%d cores", tel.Cores), false, t*0.05)
	c.gradText(x+3, y+1, fmt.Sprintf("%3.0f%%", cpu), []col{heatCol(0), heatCol(cpu)}, 0, true)
	if tel.Temp > 0 {
		s := fmt.Sprintf("%.0f°C", tel.Temp)
		c.ink(x+w1-3-rl(s), y+1, s, heatCol(tel.Temp-20), false)
	}
	c.bar(x+3, y+2, w1-6, cpu/100, heat, 0)
	c.spark2(x+3, y+3, w1-6, tel.CPUHist, 100, heat)
	c.ink(x+3, y+5, fmt.Sprintf("load  %.2f  %.2f  %.2f", tel.Load[0], tel.Load[1], tel.Load[2]), cDim, false)

	// Memory
	mem := a.smooth("mem", tel.MemPct, 0.2)
	c.panel(x+w1+1, y, w1, topH, "Memory", fmt.Sprintf("swap %.0f%%", tel.SwapPct), false, t*0.05)
	c.gradText(x+w1+4, y+1, fmt.Sprintf("%.1f", tel.MemUsed), cool, 0, true)
	c.ink(x+w1+4+rl(fmt.Sprintf("%.1f", tel.MemUsed))+1, y+1, fmt.Sprintf("/ %.1f GiB", tel.MemTotal), cDim, false)
	s := fmt.Sprintf("%.0f%%", mem)
	c.ink(x+2*w1-2-rl(s), y+1, s, heatCol(mem), true)
	c.bar(x+w1+4, y+2, w1-6, mem/100, heat, 0)
	c.spark2(x+w1+4, y+3, w1-6, tel.MemHist, 100, cool)
	c.ink(x+w1+4, y+5, "last few minutes", cFaint, false)

	// Network
	nx := x + 2*w1 + 2
	c.panel(nx, y, w3, topH, "Network", "", false, t*0.05)
	mr := math.Max(math.Max(maxOf(tel.RxHist), maxOf(tel.TxHist)), 1e5)
	c.ink(nx+3, y+1, "↓", cCyan, true)
	c.gradText(nx+5, y+1, humanRate(a.smooth("rx", tel.RxRate, 0.25)), cool, 0, true)
	c.spark(nx+3, y+2, w3-6, tel.RxHist, mr, cool)
	c.ink(nx+3, y+3, "↑", cPink, true)
	c.gradText(nx+5, y+3, humanRate(a.smooth("tx", tel.TxRate, 0.25)), warm, 0, true)
	c.spark(nx+3, y+4, w3-6, tel.TxHist, mr, warm)
	c.ink(nx+3, y+5, "physical interfaces", cFaint, false)

	// Containers
	y2 := y + topH + 1
	h2 := h - topH - 1
	lw := w * 58 / 100
	run := 0
	for _, ct := range tel.Containers {
		if ct.State == "running" {
			run++
		}
	}
	right := "docker unavailable"
	if tel.DockerOK {
		right = fmt.Sprintf("%d of %d running", run, len(tel.Containers))
	}
	c.panel(x, y2, lw, h2, "Containers", right, false, t*0.05)
	a.drawContainerRows(c, x+2, y2+1, lw-4, h2-2, tel.Containers, -1, 0)
	if left := h2 - 2 - len(tel.Containers) - 1; left >= 4 {
		amp := 0.18 + 0.5*a.smooth("amp", tel.CPU/100, 0.1) + 0.3*clamp(tel.RxRate/5e6, 0, 1)
		a.drawWave(c, x+2, y2+h2-1-left, lw-4, left, amp)
	}

	// Storage + host
	rx := x + lw + 1
	rw := w - lw - 1
	sh := 7
	if h2 < 15 {
		sh = h2 / 2
	}
	c.panel(rx, y2, rw, sh, "Storage", "", false, t*0.05)
	a.diskRow(c, rx+3, y2+1, rw-6, "system  /", tel.RootPct, tel.RootUsed, tel.RootTotal, "root")
	a.diskRow(c, rx+3, y2+3, rw-6, "media", tel.MediaPct, tel.MediaUsed, tel.MediaTotal, "media")
	hh := h2 - sh - 1
	if hh >= 5 {
		hy := y2 + sh + 1
		c.panel(rx, hy, rw, hh, "Host", "", false, t*0.05)
		rows := [][2]string{
			{"kernel", tel.Kernel},
			{"address", tel.IP},
			{"uptime", humanUp(tel.Uptime)},
			{"pulls", map[bool]string{true: "one layer at a time", false: "parallel (default)"}[tel.SingleLayer]},
		}
		for i, r := range rows {
			if i >= hh-2 {
				break
			}
			c.ink(rx+1, y2+sh+2+i, strings.Repeat(" ", max(rw-2, 0)), cDim, false)
			c.ink(rx+3, y2+sh+2+i, r[0], cFaint, false)
			c.ink(rx+13, y2+sh+2+i, fit(r[1], rw-16), cText, false)
		}
		if hh >= 13 && rw >= 32 {
			c.ink(rx+3, hy+6, tabSceneName(a.tab), cDim, false)
			a.drawTabScene(c, rx+3, hy+7, rw-6, hh-8)
		} else if hh >= 12 && rw >= 32 {
			a.drawNebula(c, rx+2, hy+6, rw-4, hh-7)
		}
	}
}

// drawNebula is decorative: it only uses space below the host information.
func (a *App) drawNebula(c *canvas, x, y, w, h int) {
	if w < 16 || h < 4 {
		return
	}
	t := a.t()
	c.panel(x, y, w, h, "Nebula", "", false, t*0.03)
	for j := 1; j < h-1; j++ {
		for i := 1; i < w-1; i++ {
			u := float64(i)/float64(w-1)*2 - 1
			v := (float64(j)/float64(h-1)*2 - 1) * 1.7
			drift := 0.16 * math.Sin(t*0.21+v*2)
			cloud := math.Exp(-((u-drift)*(u-drift)*2.5 + v*v*0.65))
			wisps := 0.55 + 0.45*math.Sin(u*7-v*3-t*0.55+math.Sin(v*4+t*0.2))
			intensity := math.Floor(clamp(cloud*(0.3+0.7*wisps), 0, 1)*10) / 10
			p := c.at(x+i, y+j)
			if p == nil {
				continue
			}
			base := glassAt(x+i, y+j, t)
			hue := mix(cViolet, cCyan, 0.35+0.25*math.Sin(u*3+t*0.12))
			p.bg = mix(base, hue, intensity*0.6)
			if hash2(i+93, j+17)%31 == 0 {
				twinkle := 0.5 + 0.5*math.Sin(t*1.7+float64(hash2(j, i)%628)/100)
				p.ch = '·'
				if twinkle > 0.78 {
					p.ch = '.'
				}
				p.fg = mix(cDim, cWhite, 0.25+0.7*twinkle)
			} else {
				p.ch = ' '
			}
		}
	}
}

// drawWave paints a slow aurora tide whose height follows machine activity.
func (a *App) drawWave(c *canvas, x, y, w, h int, amp float64) {
	t := a.t()
	for i := 0; i < w; i++ {
		fx := float64(i)
		v := 0.5 + 0.28*math.Sin(fx*0.11+t*1.3) + 0.2*math.Sin(fx*0.047-t*0.8+1.7) + 0.12*math.Sin(fx*0.31+t*2.1)
		v = clamp(v*amp*1.5, 0.04, 1) * float64(h)
		colr := cycle(aurora, fx/float64(w)*0.9+t*0.06)
		for r := 0; r < h; r++ {
			row := y + h - 1 - r
			rem := v - float64(r)
			switch {
			case rem >= 1:
				c.ink(x+i, row, "█", mix(colr, cBgB, 0.35+0.5*float64(r)/float64(h)), false)
			case rem > 0.1:
				c.ink(x+i, row, string(blocks[int(rem*8)%8]), colr, false)
			}
		}
	}
}

func maxOf(d []float64) float64 {
	m := 0.0
	for _, v := range d {
		m = math.Max(m, v)
	}
	return m
}

func (a *App) diskRow(c *canvas, x, y, w int, label string, pct, used, total float64, key string) {
	p := a.smooth("disk-"+key, pct, 0.2)
	c.ink(x, y, label, cText, false)
	s := fmt.Sprintf("%.0f / %.0f GiB   %.0f%%", used, total, pct)
	if total <= 0 {
		s = "not found"
	}
	c.ink(x+w-rl(s), y, s, cDim, false)
	c.bar(x, y+1, w, p/100, heat, 0)
}

func stateColor(s string) col {
	switch s {
	case "running":
		return cMint
	case "restarting", "paused":
		return cAmber
	case "created", "exited":
		return rgb(153, 148, 211)
	case "dead":
		return cCoral
	}
	return cDim
}

func (a *App) drawContainerRows(c *canvas, x, y, w, h int, list []Container, sel int, top int) {
	if len(list) == 0 {
		msg := "No containers found"
		if !a.tel.DockerOK && a.tel.DockerErr != "" {
			msg = "Docker is not reachable"
		}
		c.ink(x+1, y+1, msg, cDim, false)
		return
	}
	nameW := 18
	imgW := max(0, w-nameW-26)
	for i := 0; i < h && top+i < len(list); i++ {
		ct := list[top+i]
		col := stateColor(ct.State)
		yy := y + i
		c.ink(x, yy, strings.Repeat(" ", max(w, 0)), cDim, false)
		c.ink(x, yy, "▌", col, false)
		nm := cText
		if top+i == sel {
			nm = cWhite
		}
		c.ink(x+2, yy, fit(ct.Name, nameW), nm, top+i == sel)
		if imgW > 6 {
			c.ink(x+2+nameW+1, yy, fit(shortImage(ct.Image), imgW), cFaint, false)
		}
		st := fit(ct.State, 10)
		c.ink(x+w-24, yy, st, col, false)
		c.ink(x+w-13, yy, fit(upText(ct.Status), 13), cDim, false)
	}
}

func shortImage(s string) string {
	if i := strings.LastIndex(s, "/"); i >= 0 {
		s = s[i+1:]
	}
	return strings.TrimSuffix(s, ":latest")
}

func upText(s string) string {
	s = strings.TrimPrefix(s, "Up ")
	if i := strings.Index(s, " ("); i > 0 {
		s = s[:i]
	}
	return s
}

// ───────────────────────── lists ─────────────────────────

func (a *App) drawList(c *canvas, x, y, w, h int, items []listItem, key string, itemH int) {
	t := a.t()
	sel := a.sel[a.tab]
	vis := h / itemH
	top := 0
	if len(items) > vis {
		top = int(clamp(float64(sel-vis/2), 0, float64(len(items)-vis)))
	}
	ds := a.smooth("sel-"+key, float64(sel), 0.35)
	if !a.cfg.Animations {
		ds = float64(sel)
	}
	hy := y + int((ds-float64(top))*float64(itemH)+0.5)
	if hy >= y-itemH && hy < y+h {
		for j := 0; j < itemH; j++ {
			yy := hy + j
			if yy < y || yy >= y+h {
				continue
			}
			for i := -1; i < w+1; i++ {
				if p := c.at(x+i, yy); p != nil {
					p.bg = mix(glassAt(x+i, yy, t), cViolet, 0.2)
				}
			}
			c.ink(x-1, yy, "▌", cycle(aurora, float64(j)*0.2+t*0.1), false)
		}
	}
	for i := 0; i < vis && top+i < len(items); i++ {
		it := items[top+i]
		yy := y + i*itemH
		for row := 0; row < itemH && yy+row < y+h; row++ {
			c.ink(x, yy+row, strings.Repeat(" ", max(w, 0)), cDim, false)
		}
		on := top+i == sel
		a.hits = append(a.hits, hit{x - 1, yy, w + 2, itemH, "item", top + i})
		tc := cText
		if on {
			tc = cWhite
		}
		if it.barCol.set {
			c.ink(x, yy, "▌", it.barCol, false)
		}
		off := 0
		if it.barCol.set {
			off = 2
		}
		c.ink(x+off, yy, fit(it.title, w-off-rl(it.tag)-1), tc, on)
		if it.tag != "" {
			c.ink(x+w-rl(it.tag), yy, it.tag, it.tagCol, false)
		}
		if itemH > 1 {
			c.ink(x+off, yy+1, fit(it.sub, w-off), cDim, false)
		}
	}
	if len(items) > vis { // thin scroll rail
		for j := 0; j < h; j++ {
			c.ink(x+w, y+j, "│", mix(cLine, cBgA, 0.5), false)
		}
		th := max(1, h*vis/len(items))
		ty := y + (h-th)*top/max(1, len(items)-vis)
		for j := 0; j < th; j++ {
			c.ink(x+w, ty+j, "┃", cViolet, false)
		}
	}
}

func (a *App) listWidth(total int) int {
	return int(clamp(float64(total)*0.34, 38, 50))
}

// ───────────────────────── pages ─────────────────────────

func (a *App) drawOps(c *canvas, x, y, w, h int) {
	tb := tabs[a.tab]
	lw := a.listWidth(w)
	sel := a.sel[a.tab]
	items := make([]listItem, len(tb.Ops))
	for i, op := range tb.Ops {
		it := listItem{title: op.Title, sub: op.Desc}
		if j := a.jobs[tb.Name+"/"+op.ID]; j != nil {
			j.mu.Lock()
			switch {
			case j.Running:
				it.tag, it.tagCol = spinner(a.now), cViolet
			case j.Code == 0:
				it.tag, it.tagCol = "✓", cMint
			case j.Code == 130:
				it.tag, it.tagCol = "■", cAmber
			default:
				it.tag, it.tagCol = "✗", cCoral
			}
			j.mu.Unlock()
		}
		if op.Danger {
			it.barCol = cCoral
		}
		items[i] = it
	}
	c.panel(x, y, lw, h, tb.Name, fmt.Sprintf("%d", len(tb.Ops)), true, a.t()*0.05)
	ih := 2
	if len(items)*3 <= h-2 {
		ih = 3
	}
	a.drawList(c, x+3, y+1, lw-6, h-2, items, tb.Name, ih)
	op := tb.Ops[sel]
	a.drawOutput(c, x+lw+1, y, w-lw-1, h, tb.Name+"/"+op.ID, op.Title, &op)
}

func (a *App) drawContainers(c *canvas, x, y, w, h int) {
	a.mu.Lock()
	list := append([]Container(nil), a.tel.Containers...)
	a.mu.Unlock()
	lw := a.listWidth(w)
	sel := a.sel[a.tab]
	if sel >= len(list) {
		sel = max(0, len(list)-1)
		a.sel[a.tab] = sel
	}
	items := make([]listItem, len(list))
	for i, ct := range list {
		items[i] = listItem{title: ct.Name, tag: ct.State, tagCol: stateColor(ct.State), barCol: stateColor(ct.State)}
	}
	run := 0
	for _, ct := range list {
		if ct.State == "running" {
			run++
		}
	}
	c.panel(x, y, lw, h, "Containers", fmt.Sprintf("%d/%d", run, len(list)), true, a.t()*0.05)
	if len(list) == 0 {
		c.ink(x+3, y+2, "No containers found.", cDim, false)
		c.ink(x+3, y+3, "Is Docker running?", cFaint, false)
		return
	}
	a.drawList(c, x+3, y+1, lw-6, h-2, items, "containers", 1)
	ct := list[sel]
	rx, rw := x+lw+1, w-lw-1
	c.panel(rx, y, rw, 8, "Details", "↵ actions", false, a.t()*0.05)
	c.gradText(rx+3, y+1, ct.Name, aurora, a.t()*0.1, true)
	rows := [][2]string{{"image", ct.Image}, {"status", ct.Status}, {"ports", orDash(ct.Ports)}, {"compose", orDash(ct.Service)}}
	for i, r := range rows {
		c.ink(rx+3, y+3+i, r[0], cFaint, false)
		col := cText
		if r[0] == "status" {
			col = stateColor(ct.State)
		}
		c.ink(rx+12, y+3+i, fit(r[1], rw-16), col, false)
	}
	a.drawOutput(c, rx, y+9, rw, h-9, "container/"+ct.Name, "Output", nil)
}

func orDash(s string) string {
	if s == "" {
		return "—"
	}
	return s
}

type setting struct{ title, sub, val string }

func (a *App) settingsList() []setting {
	anim := map[bool]string{true: "on", false: "off"}[a.cfg.Animations]
	a.mu.Lock()
	sl := a.tel.SingleLayer
	a.mu.Unlock()
	return []setting{
		{"Animations", "Aurora, comet, transitions", anim},
		{"Telemetry refresh", "How often Docker is polled", fmt.Sprintf("%ds", a.cfg.RefreshSecond)},
		{"Update pacing", "Rest between services", fmt.Sprintf("%ds", a.cfg.PullPause)},
		{"Single-layer pulls", "Docker downloads one layer at once", map[bool]string{true: "on", false: "off"}[sl]},
		{"Keyboard guide", "Every shortcut", ""},
	}
}

func (a *App) drawSettings(c *canvas, x, y, w, h int) {
	lw := a.listWidth(w)
	sel := a.sel[a.tab]
	list := a.settingsList()
	items := make([]listItem, len(list))
	for i, s := range list {
		items[i] = listItem{title: s.title, sub: s.sub, tag: s.val, tagCol: cCyan}
	}
	c.panel(x, y, lw, h, "Settings", "", true, a.t()*0.05)
	a.drawList(c, x+3, y+1, lw-6, h-2, items, "settings", 3)

	rx, rw := x+lw+1, w-lw-1
	if j := a.jobs["settings/single"]; j != nil && (sel == 3) {
		a.drawOutput(c, rx, y, rw, h, "settings/single", "Single-layer pulls", nil)
		return
	}
	c.panel(rx, y, rw, h, list[sel].title, "", false, a.t()*0.05)
	help := map[int]string{
		0: "Turns the aurora background, comet, shimmer and page transitions on or off. Switch them off on a very slow terminal or over a weak SSH link.",
		1: "How often homelab asks Docker for container state. Press ↵ to cycle 2, 5 and 10 seconds. CPU, memory and network always sample once per second at almost no cost.",
		2: "Updates run one service at a time. After each one, homelab waits until the machine is calm and then rests for this long. Press ↵ to cycle 0, 5, 8, 15 and 30 seconds.",
		3: "Docker normally downloads three image layers at once. This writes max-concurrent-downloads and uploads = 1 to /etc/docker/daemon.json and reloads Docker without restarting containers. A backup is kept next to the file.",
		4: "Opens the full list of shortcuts.",
	}[sel]
	yy := y + 2
	for _, ln := range wrapText(help, rw-6) {
		c.ink(rx+3, yy, ln, cText, false)
		yy++
	}
	yy++
	c.ink(rx+3, yy, "CONFIGURATION", cFaint, true)
	yy++
	cfg := [][2]string{
		{"file", configPath()}, {"compose", a.cfg.ComposeFile}, {"arr", a.cfg.ArrDir},
		{"media", a.cfg.MediaDir}, {"backups", a.cfg.BackupDir}, {"reports", a.cfg.ReportDir},
	}
	for _, r := range cfg {
		if yy >= y+h-1 {
			break
		}
		c.ink(rx+3, yy, r[0], cFaint, false)
		c.ink(rx+13, yy, fit(r[1], rw-17), cText, false)
		yy++
	}
	if available := y + h - 2 - (yy + 1); available >= 5 {
		a.drawLabeledScene(c, rx+3, yy+1, rw-6, available)
	}
}

// ───────────────────────── output panel ─────────────────────────

var (
	reErr  = regexp.MustCompile(`(?i)\b(error|failed|denied|fatal|cannot|unable|refused|unhealthy)\b`)
	reWarn = regexp.MustCompile(`(?i)\b(warn|warning|deprecated)\b`)
)

func lineColor(s string) (col, bool) {
	ts := strings.TrimSpace(s)
	switch {
	case strings.HasPrefix(ts, "▸"):
		return cCyan, true
	case strings.HasPrefix(ts, "✓"), strings.Contains(s, "VERIFIED"), strings.HasPrefix(ts, "Saved:"):
		return cMint, false
	case strings.HasPrefix(ts, "✗"):
		return cCoral, false
	case strings.HasPrefix(ts, "■"):
		return cAmber, false
	case strings.HasPrefix(ts, "──"), strings.HasPrefix(ts, "==="):
		return cPink, true
	case reErr.MatchString(s):
		return cCoral, false
	case reWarn.MatchString(s):
		return cAmber, false
	}
	return cText, false
}

func spinner(t time.Time) string {
	fr := []string{"⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"}
	return fr[int(t.UnixMilli()/80)%len(fr)]
}

func (a *App) drawOutput(c *canvas, x, y, w, h int, key, title string, op *Op) {
	j := a.jobs[key]
	status := ""
	if j != nil {
		j.mu.Lock()
		if j.Running {
			status = "running " + fmtDur(a.now.Sub(j.Start))
		} else {
			status = fmtDur(j.End.Sub(j.Start))
		}
		j.mu.Unlock()
	}
	c.panel(x, y, w, h, title, status, j != nil && j.Running, a.t()*0.2)
	ix, iw := x+3, w-6
	if j == nil {
		a.drawIdle(c, ix, y+2, iw, h-4, op, key)
		return
	}
	j.mu.Lock()
	defer j.mu.Unlock()
	row := y + 1
	// status line
	switch {
	case j.Running:
		c.ink(ix, row, spinner(a.now), cViolet, true)
		lbl := j.Title
		if j.StepLabel != "" {
			lbl = j.StepLabel
		}
		c.gradText(ix+2, row, fit(lbl, iw-24), aurora, a.t()*0.4, true)
		if j.Steps > 0 {
			s := fmt.Sprintf("step %d of %d", j.Step, j.Steps)
			c.ink(ix+iw-rl(s), row, s, cDim, false)
		}
	case j.Code == 0:
		c.ink(ix, row, "✓ finished", cMint, true)
	case j.Code == 130:
		c.ink(ix, row, "■ stopped", cAmber, true)
	default:
		c.ink(ix, row, fmt.Sprintf("✗ exited with code %d", j.Code), cCoral, true)
	}
	row++
	if j.Steps > 0 {
		f := float64(j.Step-1) / float64(j.Steps)
		if !j.Running {
			f = 1
		}
		c.bar(ix, row, iw, a.smooth("prog-"+key, f, 0.2), aurora, 0)
		row++
	}
	if j.Note != "" {
		c.ink(ix, row, "◌ "+j.Note, cAmber, false)
		row++
	}
	row++
	bodyH := y + h - 1 - row
	if bodyH < 1 {
		return
	}
	n := len(j.Lines)
	maxScroll := max(0, n-bodyH)
	if j.Scroll > maxScroll {
		j.Scroll = maxScroll
	}
	start := max(0, n-bodyH-j.Scroll)
	for i := 0; i < bodyH && start+i < n; i++ {
		ln := j.Lines[start+i]
		fg, bold := lineColor(ln)
		c.ink(ix, row+i, fit(ln, iw), fg, bold)
	}
	if j.Scroll > 0 {
		s := fmt.Sprintf("↑ %d more below", j.Scroll)
		c.ink(x+w-3-rl(s), y+h-1, s, cViolet, false)
	}
	a.hits = append(a.hits, hit{x, y, w, h, "out", 0})
}

func fmtDur(d time.Duration) string {
	s := int(d.Seconds())
	if s >= 3600 {
		return fmt.Sprintf("%d:%02d:%02d", s/3600, s/60%60, s%60)
	}
	return fmt.Sprintf("%02d:%02d", s/60, s%60)
}

func (a *App) drawIdle(c *canvas, x, y, w, h int, op *Op, key string) {
	if op == nil {
		c.ink(x, y, "Press ↵ for actions — their output lands here.", cDim, false)
		if wh := min(20, h-3); wh >= 5 {
			a.drawLabeledScene(c, x, y+h-1-wh, w, wh)
		} else if wh >= 3 {
			a.drawWave(c, x, y+h-1-wh, w, wh, 0.22)
		}
		return
	}
	yy := y
	c.ink(x, yy, op.Desc, cText, true)
	yy += 2
	if op.Special != "" {
		for _, ln := range wrapText(op.Confirm, w) {
			c.ink(x, yy, ln, cDim, false)
			yy++
		}
		yy++
		c.ink(x, yy, "Never more than one container at a time.", cFaint, false)
		yy++
		c.ink(x, yy, "Runs at the lowest CPU and disk priority.", cFaint, false)
		yy++
		c.ink(x, yy, "Pauses between services if the machine is busy.", cFaint, false)
		yy += 2
	} else {
		c.ink(x, yy, "RUNS", cFaint, true)
		yy++
		n := 0
		for _, ln := range strings.Split(strings.ReplaceAll(op.Cmd, "; ", ";\n"), "\n") {
			if yy >= y+h-3 || n >= 4 {
				break
			}
			c.ink(x, yy, fit("$ "+strings.TrimSpace(ln), w), cFaint, false)
			yy++
			n++
		}
		yy++
	}
	if wh := min(20, (y+h-1)-yy-1); wh >= 5 {
		a.drawLabeledScene(c, x, y+h-2-wh, w, wh)
	} else if wh >= 3 {
		a.drawWave(c, x, y+h-2-wh, w, wh, 0.22)
	}
	pulse := 0.5 + 0.5*math.Sin(a.t()*2.4)
	hint := "press ↵ to run"
	if op.Danger {
		c.ink(x, y+h-1, hint, mix(cFaint, cCoral, pulse), true)
		return
	}
	c.ink(x, y+h-1, hint, mix(cFaint, cCyan, pulse), true)
}

// ───────────────────────── overlays ─────────────────────────

func (a *App) drawModal(c *canvas) {
	m := a.modal
	c.dim(0, 0, c.w, c.h, 0.62)
	w := 62
	var lines []string
	if m.Help {
		w = 66
	}
	for _, b := range m.Body {
		if m.Help {
			lines = append(lines, strings.Split(b, "\n")...)
		} else {
			lines = append(lines, wrapText(b, w-6)...)
		}
	}
	fullH := 4 + len(lines)
	if len(m.Items) > 0 {
		fullH += len(m.Items) + 1
	}
	if m.Confirm {
		fullH += 2
	}
	p := ease(float64(a.now.Sub(m.Opened)) / float64(220*time.Millisecond))
	if !a.cfg.Animations {
		p = 1
	}
	hh := max(3, int(3+float64(fullH-3)*p))
	x, y := (c.w-w)/2, (c.h-hh)/2
	if m.Danger {
		c.tint = cCoral
	} else {
		c.tint = cViolet
	}
	c.panel(x, y, w, hh, m.Title, "", false, 0)
	c.tint = col{}
	c.resolve()
	if p < 0.55 {
		return
	}
	yy := y + 2
	for _, ln := range lines {
		if yy >= y+hh-1 {
			break
		}
		c.ink(x+3, yy, ln, cText, false)
		yy++
	}
	if len(m.Items) > 0 {
		yy++
		for i, it := range m.Items {
			if yy >= y+hh-1 {
				break
			}
			a.hits = append(a.hits, hit{x + 2, yy, w - 4, 1, "modal", i})
			if i == m.Idx {
				for k := 0; k < w-4; k++ {
					if pc := c.at(x+2+k, yy); pc != nil {
						pc.bg = mix(pc.bg, cViolet, 0.3)
					}
				}
				c.ink(x+3, yy, "▸ "+it, cWhite, true)
			} else {
				c.ink(x+5, yy, it, cDim, false)
			}
			yy++
		}
	}
	if m.Confirm && hh >= fullH {
		by := y + hh - 2
		d := cMint
		if m.Danger {
			d = cCoral
		}
		xx := x + 3
		xx += c.ink(xx, by, "↵ confirm", d, true) + 4
		c.ink(xx, by, "esc cancel", cDim, false)
	}
}

func (a *App) helpModal() *Modal {
	return &Modal{Title: "Keyboard", Help: true, Opened: a.now, Body: []string{
		"←  →  or  Tab         switch tab\n1 – 9, 0               jump to a tab\n↑  ↓  or  j  k         move selection\n↵  Enter               run, open or toggle\nPgUp  PgDn             scroll the output\ng  G                   top / bottom of output\nx                      stop the running task\nr                      refresh now\nmouse wheel / click    scroll and select\n?                      toggle this guide\nq  Ctrl+C              quit",
	}}
}
