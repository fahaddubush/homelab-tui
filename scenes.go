package main

import "math"

// These are stylised illustrations, not telemetry.
func tabSceneName(tab int) string {
	names := [...]string{"Spiral Galaxy", "Ringed Planet", "Accretion Disk", "Asteroid Belt", "Pulsar", "Einstein Ring", "Binary Stars", "Meteor Shower", "Solar Corona", "Cosmic Web"}
	if tab < 0 || tab >= len(names) {
		return "Deep Space"
	}
	return names[tab]
}

// drawTabScene supersamples each cell and paints its background. Using spaces
// avoids terminal minimum-contrast rules that brighten half-block foregrounds.
// Objects use the approximate 1:2 cell aspect ratio.
func (a *App) drawTabScene(c *canvas, x, y, w, h int) {
	if w < 16 || h < 4 {
		return
	}
	t := math.Floor(a.t()*8) / 8
	for j := 0; j < h; j++ {
		for i := 0; i < w; i++ {
			p := c.at(x+i, y+j)
			if p == nil {
				continue
			}
			base := a.ambientBackground(c, x+i, y+j, t, true)
			edge := clamp(math.Min(math.Min(float64(i), float64(w-1-i))/5, math.Min(float64(j)+0.5, float64(h-j)-0.5)/1.5), 0, 1)
			sample := func(sy float64) col {
				// Horizontal oversampling softens the outlines of small planets.
				u1 := (float64(i) + 0.28 - float64(w)/2) / float64(h)
				u2 := (float64(i) + 0.72 - float64(w)/2) / float64(h)
				v := 2 * (float64(j) + sy - float64(h)/2) / float64(h)
				return mix(a.cosmicPixel(base, u1, v, t, edge, i, j), a.cosmicPixel(base, u2, v, t, edge, i, j), 0.5)
			}
			top, bottom := sample(0.25), sample(0.75)
			*p = cell{ch: ' ', fg: cDim, bg: mix(top, bottom, 0.5)}
			// Stars remain tiny pinpoints instead of bright, rectangular pixels.
			seed := hash2(i+113+a.tab*31, j+97)
			brightness := int(top.r) + int(top.g) + int(top.b)
			if seed%67 == 0 && brightness < 150 && edge > 0.4 {
				pulse := 0.5 + 0.5*math.Sin(t*0.9+float64(seed%628)/100)
				*p = cell{ch: '·', fg: mix(cDim, cWhite, 0.15+0.35*pulse), bg: mix(top, bottom, 0.5)}
			}
		}
	}
}

func gauss(v, width float64) float64                 { v /= width; return math.Exp(-v * v) }
func smoothDisc(r, radius, softness float64) float64 { return clamp((radius-r)/softness+0.5, 0, 1) }

// cosmicPixel composes light from back to front. All visual time is supplied by
// a.t(), so disabling animation freezes the entire scene.
func (a *App) cosmicPixel(base col, u, v, t, edge float64, i, j int) col {
	// Expand the subject across the available scene while preserving the cell
	// aspect ratio. The edge falloff still blends it into surrounding glass.
	u *= 0.67
	v *= 0.87
	bg := base
	paint := func(color col, amount float64) { bg = mix(bg, color, clamp(amount*edge, 0, 0.86)) }
	// A restrained depth field: stable scattered pinpoints and slow, layered dust.
	dust := gauss(v-0.24*math.Sin(u*1.9+t*0.08), 0.48) * gauss(u, 1.8)
	paint(mix(cBgC, cViolet, 0.42), dust*0.11)
	angle := math.Atan2(v, u)
	switch a.tab {
	case 0: // A tilted spiral with a dusty, luminous central bulge.
		ry := v / 0.47
		r := math.Hypot(u, ry)
		arm := math.Pow(math.Max(0, math.Cos(2*math.Atan2(ry, u)-r*5.2+t*0.14)), 5)
		cloud := gauss(u+0.55+0.1*math.Sin(t*0.1), 0.85) * gauss(v-0.3, 0.45)
		paint(cPink, cloud*0.16)
		paint(mix(cViolet, cCyan, 0.5), arm*gauss(r, 1.2)*0.66)
		paint(cPink, gauss(ry-0.12*math.Sin(u*3+t*0.08), 0.13)*gauss(u, 1.05)*0.13)
		dustLane := gauss(ry+0.16+0.10*math.Sin(u*4), 0.06) * gauss(u, 1.0)
		bg = mix(bg, cBgA, dustLane*0.16*edge)
		paint(cWhite, gauss(r, 0.16)*0.75)
	case 1: // A shaded sphere hides the rear arc; front rings cross its face.
		r := math.Hypot(u, v)
		planet := smoothDisc(r, 0.58, 0.10)
		ringR := math.Hypot((u+v*0.15)/1.18, v/0.34)
		ring := gauss(ringR-0.78, 0.08) * 0.60
		if v < 0 || planet < 0.01 {
			paint(cCyan, ring)
		}
		if planet > 0 {
			nx, ny := u/0.58, v/0.58
			nz := math.Sqrt(math.Max(0, 1-nx*nx-ny*ny))
			light := clamp(0.24+0.50*nz-0.28*nx-0.15*ny, 0.10, 0.85)
			bands := 0.5 + 0.5*math.Sin(v*19+t*0.26+0.8*math.Sin(u*3))
			paint(mix(cViolet, cCyan, 0.18+0.30*bands), planet*light)
			// A gentle storm and illuminated atmospheric edge rotate with the bands.
			storm := gauss(u-0.15-0.13*math.Sin(t*0.15), 0.12) * gauss(v+0.08, 0.075)
			paint(cPink, planet*storm*0.18)
			paint(cCyan, gauss(r-0.56, 0.045)*planet*0.16)
		}
		if v >= 0 {
			paint(cCyan, ring*0.85)
		}
		mx, my := 1.18*math.Cos(t*0.15), 0.56*math.Sin(t*0.15)
		moon := smoothDisc(math.Hypot(u-mx, v-my), 0.10, 0.07)
		if planet < 0.1 || my > 0 {
			paint(cWhite, moon*0.34)
		}
	case 2: // Lensed accretion arcs, an occluding shadow and polar jets.
		disk := math.Hypot(u/1.12, v/0.27)
		glow := gauss(disk-0.65, 0.16) * (0.66 + 0.25*math.Sin(u*6-t*0.7))
		paint(mix(cPink, cCyan, clamp(0.5+u*0.25, 0, 1)), glow*0.70)
		hotspot := gauss(u-0.55*math.Cos(t*0.28), 0.13) * gauss(v-0.16*math.Sin(t*0.28), 0.08)
		paint(cWhite, hotspot*0.28)
		paint(cCyan, gauss(u, 0.12)*gauss(math.Abs(v)-0.56, 0.36)*0.25)
		shadow := smoothDisc(math.Hypot(u, v), 0.22, 0.08)
		bg = mix(bg, cBgA, shadow*0.92)
		paint(cWhite, gauss(disk-0.40, 0.06)*0.30)
	case 3: // Several orbital dust bands and foreground stones.
		band := gauss(v-0.20*math.Sin(u*2.1+t*0.09), 0.27)
		paint(cViolet, band*0.22)
		paint(cCyan, gauss(v+0.12*math.Sin(u*3+t*0.12)-0.15, 0.12)*gauss(u, 1.5)*0.09)
		for n := 0; n < 5; n++ {
			phase := t*(0.025+float64(n)*0.004) + float64(n)*1.36
			px := 1.5 * math.Sin(phase)
			py := 0.25 * math.Cos(phase*1.2+float64(n))
			rock := smoothDisc(math.Hypot(u-px, v-py), 0.07+0.015*float64(n%2), 0.06)
			paint(mix(cAmber, cViolet, 0.5), rock*(0.32+0.08*float64(n%3)))
			paint(cWhite, gauss(math.Hypot(u-px+0.025, v-py+0.025), 0.035)*rock*0.22)
		}
	case 4: // Rotating pulsar beams within expanding, softer shock rings.
		r := math.Hypot(u, v)
		pulse := 0.6 + 0.4*math.Sin(t*0.9)
		wave := math.Abs(math.Mod(r-t*0.12+20, 0.33) - 0.165)
		paint(cCyan, gauss(wave, 0.045)*gauss(r, 1.4)*0.28)
		beam := gauss(math.Sin(angle-t*0.24), 0.12) * gauss(r, 1.15)
		paint(cViolet, beam*0.31)
		paint(cPink, gauss(r-0.58, 0.20)*gauss(math.Sin(angle*2+t*0.22), 0.35)*0.10)
		paint(cCyan, gauss(r, 0.16)*(0.5+0.15*pulse))
		paint(cWhite, gauss(r, 0.045)*0.72)
	case 5: // A foreground galaxy lenses a distant source into luminous arcs.
		r := math.Hypot(u, v/0.72)
		ring := gauss(r-0.58, 0.055) * (0.72 + 0.28*math.Cos(angle*2+t*0.18))
		paint(mix(cCyan, cPink, 0.5+0.2*math.Sin(angle)), ring*0.68)
		paint(cCyan, gauss(r-0.77, 0.045)*gauss(math.Sin(angle+t*0.13), 0.6)*0.19)
		paint(cAmber, gauss(r, 0.22)*0.32)
		paint(cWhite, gauss(r, 0.045)*0.45)
	case 6: // Two stars, each with a halo, cross through different depths.
		ang := t * 0.20
		sx, sy := 0.46*math.Cos(ang), 0.34*math.Sin(ang)
		orbit := math.Hypot(u/0.52, v/0.38)
		paint(cViolet, gauss(orbit-1, 0.08)*0.13)
		r1, r2 := math.Hypot(u-sx, v-sy), math.Hypot(u+sx, v+sy)
		paint(cViolet, gauss(v-0.1*math.Sin(u*4+t*0.18), 0.12)*gauss(u, 0.55)*0.11)
		paint(cPink, gauss(r1, 0.25)*0.55)
		paint(cCyan, gauss(r2, 0.23)*0.55)
		paint(cWhite, math.Max(gauss(r1, 0.055), gauss(r2, 0.05))*0.60)
	case 7: // Three meteor trails, with foreground dust drifting at another speed.
		for n := 0; n < 4; n++ {
			phase := math.Mod(t*(0.08+float64(n)*0.015)+float64(n)*0.39, 1.6) - 0.3
			head := phase*3.4 - 1.7
			d := head - u
			line := (float64(n)-1.5)*0.28 + (u-head)*0.26
			if d >= 0 && d < 0.8 {
				paint(mix(cViolet, cWhite, 0.6), gauss(v-line, 0.05)*(1-d/0.8)*0.68)
			}
		}
	case 8: // A softly shaded solar sphere and looping corona.
		r := math.Hypot(u, v)
		corona := gauss(r-0.60, 0.22) * (0.55 + 0.45*math.Sin(angle*5+t*0.35))
		paint(mix(cPink, cAmber, 0.52), corona*0.44)
		flare := gauss(r-0.69, 0.11) * gauss(math.Sin(angle-t*0.11), 0.18)
		paint(cPink, flare*0.20)
		surface := smoothDisc(r, 0.52, 0.08)
		light := clamp(0.30+0.42*math.Sqrt(math.Max(0, 1-r*r/(0.52*0.52)))-0.18*u, 0.15, 0.8)
		paint(mix(cPink, cAmber, 0.5), surface*light)
		paint(cWhite, gauss(math.Hypot(u+0.23, v+0.20), 0.25)*surface*0.18)
	case 9: // Three curving filaments join softly glowing distant clusters.
		wave1 := math.Abs(v - 0.35*math.Sin(u*2.3+t*0.13))
		wave2 := math.Abs(v + 0.38*math.Sin(u*1.8-t*0.10+1.4))
		wave3 := math.Abs(v - 0.23*math.Sin(u*2.0+t*0.07+2.1))
		web := math.Max(gauss(wave1, 0.05), math.Max(gauss(wave2, 0.05), gauss(wave3, 0.04)))
		paint(mix(cViolet, cCyan, 0.5), web*0.33)
		paint(cCyan, gauss(math.Hypot(u-0.55, v-0.25), 0.18)*0.43)
		paint(cPink, gauss(math.Hypot(u+0.55, v+0.15), 0.19)*0.39)
		paint(cWhite, gauss(math.Hypot(u-0.1*math.Sin(t*0.15), v), 0.10)*web*0.16)
	}
	return bg
}
