package main

import (
	"fmt"
	"strings"
	"testing"
	"time"
)

func TestEveryTabSceneStaysInBoundsAndMoves(t *testing.T) {
	for tab := 0; tab < 10; tab++ {
		t.Run(fmt.Sprint(tab), func(t *testing.T) {
			a := newTestApp(t)
			a.cfg.Animations = true
			a.tab = tab
			paint := func(when time.Duration) *canvas {
				a.now = a.start.Add(when)
				c := newCanvas(80, 24)
				for i := range c.cells {
					c.cells[i].ch = ' '
				}
				c.ink(1, 1, "protected text", cText, false)
				a.drawTabScene(c, 10, 6, 50, 10)
				return c
			}
			one, two := paint(0), paint(3*time.Second)
			changed, motion := 0, 0
			for y := 0; y < one.h; y++ {
				for x := 0; x < one.w; x++ {
					p, q := one.at(x, y), two.at(x, y)
					inside := x >= 10 && x < 60 && y >= 6 && y < 16
					if !inside && *p != (cell{ch: ' '}) && !(y == 1 && x >= 1 && x < 15) {
						t.Fatalf("wrote outside scene at %d,%d", x, y)
					}
					if inside && p.bg.set {
						changed++
					}
					if inside && *p != *q {
						motion++
					}
				}
			}
			if changed == 0 || motion == 0 {
				t.Fatalf("scene has no painting or motion: %d %d", changed, motion)
			}
			if string(one.at(1, 1).ch) != "p" {
				t.Fatal("text damaged")
			}
		})
	}
}

func TestScenesFreezeWhenAnimationsOff(t *testing.T) {
	for tab := 0; tab < 10; tab++ {
		a := newTestApp(t)
		a.cfg.Animations = false
		a.tab = tab
		c, d := newCanvas(60, 12), newCanvas(60, 12)
		a.now = a.start
		a.drawTabScene(c, 0, 0, 60, 12)
		a.now = a.start.Add(20 * time.Second)
		a.drawTabScene(d, 0, 0, 60, 12)
		for i := range c.cells {
			if c.cells[i] != d.cells[i] {
				t.Fatalf("tab %d moves with animation disabled", tab)
			}
		}
	}
}

func TestSceneConfigIsIsolated(t *testing.T) {
	if !strings.Contains(configPath(), "homelab") {
		t.Fatalf("test config is not isolated: %s", configPath())
	}
}

func TestAllTabsRenderAtSupportedSizes(t *testing.T) {
	for _, size := range [][2]int{{96, 28}, {120, 32}, {160, 40}, {220, 55}} {
		for tab := 0; tab < 10; tab++ {
			a := newTestApp(t)
			a.cfg.Animations = true
			a.tab = tab
			a.tabStart = a.start.Add(-time.Hour)
			demoTelemetry(a)
			for i := 0; i < 30; i++ {
				a.tel.Containers = append(a.tel.Containers, Container{Name: fmt.Sprintf("service-%d", i), State: "exited"})
			}
			c := newCanvas(size[0], size[1])
			c.t = a.t()
			a.render(c)
			if len(c.cells) != size[0]*size[1] {
				t.Fatalf("bad frame size %v tab %d", size, tab)
			}
		}
	}
}

func TestNoDecorativeStarGlyphs(t *testing.T) {
	for tab := 0; tab < 10; tab++ {
		a := newTestApp(t)
		a.cfg.Animations = true
		a.tab = tab
		a.tabStart = a.start.Add(-time.Hour)
		demoTelemetry(a)
		c := newCanvas(150, 36)
		c.t = a.t()
		a.render(c)
		for _, p := range c.cells {
			if strings.ContainsRune("✦★✧⭐🌟", p.ch) {
				t.Fatalf("tab %d still uses decorative star %q", tab, p.ch)
			}
		}
	}
}

func TestScenesUseContrastSafeCells(t *testing.T) {
	for tab := 0; tab < 10; tab++ {
		a := newTestApp(t)
		a.cfg.Animations = true
		a.tab = tab
		c := newCanvas(80, 24)
		a.drawTabScene(c, 10, 5, 60, 12)
		colors := map[col]bool{}
		for y := 5; y < 17; y++ {
			for x := 10; x < 70; x++ {
				p := c.at(x, y)
				if p.ch != ' ' && p.ch != '·' {
					t.Fatalf("tab %d uses contrast-sensitive scene glyph %q", tab, p.ch)
				}
				if !p.bg.set {
					t.Fatalf("tab %d has unset scene background", tab)
				}
				colors[p.bg] = true
			}
		}
		if len(colors) < 4 {
			t.Fatalf("tab %d lacks shading", tab)
		}
	}
}

func TestExtrasPreserveExistingText(t *testing.T) {
	for tab := 0; tab < 10; tab++ {
		a := newTestApp(t)
		a.cfg.Animations = true
		a.tab = tab
		a.lastInput = time.Now().Add(-20 * time.Second)
		a.now = time.Now()
		c := newCanvas(150, 36)
		for i := range c.cells {
			c.cells[i].ch = 'X'
		}
		a.drawCosmicExtras(c)
		for _, p := range c.cells {
			if p.ch != 'X' {
				t.Fatalf("tab %d overwrote text", tab)
			}
		}
	}
}

func TestExtrasIdleAndReducedMotion(t *testing.T) {
	for tab := 0; tab < 10; tab++ {
		a := newTestApp(t)
		a.cfg.Animations = true
		a.tab = tab
		a.now = a.start.Add(time.Minute)
		render := func(idle time.Duration) *canvas {
			c := newCanvas(150, 36)
			for i := range c.cells {
				c.cells[i].ch = ' '
			}
			a.lastInput = a.now.Add(-idle)
			a.drawCosmicExtras(c)
			return c
		}
		before, idle := render(4*time.Second), render(6*time.Second)
		changed := 0
		for i := range before.cells {
			if before.cells[i] != idle.cells[i] {
				changed++
			}
		}
		if changed == 0 {
			t.Fatalf("tab %d has no idle event", tab)
		}
		a.cfg.Animations = false
		still := render(6 * time.Second)
		a.now = a.now.Add(30 * time.Second)
		still2 := render(20 * time.Second)
		for i := range still.cells {
			if still.cells[i] != still2.cells[i] {
				t.Fatalf("tab %d animates with motion off", tab)
			}
		}
	}
}
