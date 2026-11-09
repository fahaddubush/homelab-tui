package main

import "math"

// drawCosmicExtras paints faint, tab-specific points in unused sky. It never
// changes a background or overwrites any non-space cell, including stars,
// borders, labels, and artwork. The short events use the idle clock, so any
// interaction naturally restarts their schedule.
func (a *App) drawCosmicExtras(c *canvas) {
	if a.modal != nil || c.w < 32 || c.h < 14 {
		return
	}
	palette := [...]col{cViolet, cCyan, cPink, cAmber, cCyan, cViolet, cPink, cCyan, cAmber, cViolet}
	idx := a.tab
	if idx < 0 || idx >= len(palette) {
		return
	}
	t := a.t()
	ink := func(x, y int, strength float64) {
		if x < 2 || x >= c.w-2 || y < 5 || y >= c.h-2 {
			return
		}
		p := c.at(x, y)
		// Explicit backgrounds include the dedicated astronomical vignettes.
		if p == nil || p.ch != ' ' || p.bg.set {
			return
		}
		// Do not sprinkle punctuation directly beside text or panel borders.
		for dy := -1; dy <= 1; dy++ {
			for dx := -2; dx <= 2; dx++ {
				if dx == 0 && dy == 0 {
					continue
				}
				q := c.at(x+dx, y+dy)
				if q == nil || (q.ch != ' ' && q.ch != '.' && q.ch != '·') {
					return
				}
			}
		}
		p.ch = '.'
		p.fg = mix(cFaint, palette[idx], clamp(strength, 0, 0.55))
	}
	// A sparse layer with independent phases, not a screen-wide animation.
	for n := 0; n < 14; n++ {
		seed := hash2(idx*131+n*23+19, n*71+37)
		x := 2 + int(seed%uint32(c.w-4))
		y := 5 + int((seed/71)%uint32(c.h-7))
		shimmer := 0.5 + 0.5*math.Sin(t*(0.35+float64(seed%4)*0.12)+float64(seed%628)/100)
		if shimmer > 0.28 {
			ink(x, y, 0.16+0.25*shimmer)
		}
	}
	// A persistent, tab-specific companion pattern lives outside the main
	// vignette: clusters, orbits, wisps and signal arcs are visible while active.
	seed := hash2(idx*311+59, idx*127+23)
	cx := 8 + int(seed%uint32(c.w-16))
	cy := 7 + int((seed/71)%uint32(c.h-12))
	for n := 0; n < 18; n++ {
		u := float64(n) / 17
		var x, y float64
		switch idx % 5 {
		case 0: // star clusters and nebular knots
			ang := u*math.Pi*4 + t*0.06
			r := 1 + u*7
			x, y = float64(cx)+r*math.Cos(ang), float64(cy)+r*0.23*math.Sin(ang)
		case 1: // tiny planetary orbit and moon
			ang := u*math.Pi*2 + t*0.10
			x, y = float64(cx)+8*math.Cos(ang), float64(cy)+2*math.Sin(ang)
		case 2: // fine dust lane with drifting lights
			x = float64(cx) + float64(n) - 9
			y = float64(cy) + 1.4*math.Sin(u*math.Pi*2+t*0.12)
		case 3: // constellation of distant points
			x = float64(cx) + float64(n) - 9
			y = float64(cy) + math.Round(1.5*math.Sin(u*7+float64(idx)))
		case 4: // expanding signal rings
			ang := u * math.Pi * 2
			r := 4 + 0.6*math.Sin(t*0.2)
			x, y = float64(cx)+r*math.Cos(ang), float64(cy)+r*0.35*math.Sin(ang)
		}
		ink(int(math.Round(x)), int(math.Round(y)), 0.34)
	}
	// Short companion appearances run on their own clock, even during use.
	// Different tab offsets and cycles keep their timing from feeling synchronized.
	if a.cfg.Animations {
		period := 13.0 + float64(idx%4)*3
		phase := math.Mod(t+float64(idx)*2.3, period)
		if phase < 3.2 {
			fade := math.Sin(math.Pi * phase / 3.2)
			for n := 0; n < 12; n++ {
				u := float64(n) / 11
				x, y := float64(cx), float64(cy)
				switch idx % 3 {
				case 0: // brief star cluster emerging from haze
					ang := u * math.Pi * 2
					x += (3 + u*4) * math.Cos(ang)
					y += (0.8 + u) * math.Sin(ang)
				case 1: // distant transit or orbital arc
					ang := u*math.Pi*1.4 + phase*0.12
					x += 7 * math.Cos(ang)
					y += 2 * math.Sin(ang)
				case 2: // a soft moving trail through the dust
					x += float64(n) - 6 + phase*2
					y += math.Sin(u*4+phase) * 0.8
				}
				ink(int(math.Round(x)), int(math.Round(y)), 0.14+0.35*fade)
			}
		}
	}
	if !a.cfg.Animations || a.lastInput.IsZero() {
		return
	}
	idle := a.now.Sub(a.lastInput).Seconds()
	if idle < 5 {
		return
	}
	cycle, elapsed := 0, idle-5
	for cycle < 10000 {
		interval := 6.0 + float64(hash2(idx*173+cycle*19+31, idx+83)%6)
		if elapsed < interval {
			break
		}
		elapsed -= interval
		cycle++
	}
	phase := elapsed
	if phase >= 2.1 {
		return
	}
	seed = hash2(idx*173+cycle*19+31, idx+83)
	cx = 5 + int(seed%uint32(c.w-10))
	cy = 7 + int((seed/101)%uint32(c.h-10))
	// Each event has a slow fade at both ends and remains a small accent.
	fade := math.Sin(math.Pi * phase / 2.1)
	for n := 0; n < 15; n++ {
		u := float64(n) / 14
		var x, y float64
		switch idx {
		case 0: // Spiral dust plume.
			r := 1 + u*9
			ang := u*math.Pi*2 + phase*0.5
			x, y = float64(cx)+r*math.Cos(ang), float64(cy)+r*0.22*math.Sin(ang)
		case 1: // Planetary ring passing across the background.
			ang := u*math.Pi*2 + phase*0.15
			x, y = float64(cx)+10*math.Cos(ang), float64(cy)+2*math.Sin(ang)
		case 2: // Inward-moving, tilted accretion sparks.
			r := (1 - u) * 9
			ang := u*math.Pi*3 - phase*0.6
			x, y = float64(cx)+r*math.Cos(ang), float64(cy)+r*0.24*math.Sin(ang)
		case 3: // A drift of small distant rocks.
			x = float64(cx) + float64(n) - 7 + phase*2
			y = float64(cy) + math.Sin(u*13+float64(seed%7))*1.5
		case 4: // Soft outward-moving pulsar ripples.
			ang := u * math.Pi * 2
			r := 3 + phase*3
			x, y = float64(cx)+r*math.Cos(ang), float64(cy)+r*0.24*math.Sin(ang)
		case 5: // Paired lens arcs.
			ang := u*math.Pi*2 + 0.3*math.Sin(phase)
			x, y = float64(cx)+8*math.Cos(ang), float64(cy)+2*math.Sin(ang)
		case 6: // Two orbital arcs around a shared center.
			ang := u*math.Pi*2 + phase*0.5
			x, y = float64(cx)+7*math.Cos(ang), float64(cy)+2*math.Sin(ang)
		case 7: // A restrained diagonal shower.
			x = float64(cx) + float64(n) - 7 + phase*5
			y = float64(cy) + math.Floor(float64(n)/5) - 1
		case 8: // A pair of coronal arcs.
			ang := u * math.Pi
			x, y = float64(cx)+8*math.Cos(ang), float64(cy)-2*math.Sin(ang)
		case 9: // Wispy threads in a small cosmic web.
			x = float64(cx) + float64(n) - 7
			y = float64(cy) + 1.6*math.Sin(u*math.Pi*2+phase*0.4)
		}
		ink(int(math.Round(x)), int(math.Round(y)), 0.20+0.27*fade)
	}
}
