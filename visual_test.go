package main

import (
	"bufio"
	"bytes"
	"testing"
	"time"
)

func TestCometsRespectIdleAndContent(t *testing.T) {
	c := newCanvas(120, 36)
	for i := range c.cells {
		c.cells[i].ch = ' '
	}
	c.ink(60, 12, "IMPORTANT STATUS", cText, true)
	before := append([]cell(nil), c.cells...)
	c.idleComets(4.9)
	for i := range before {
		if c.cells[i] != before[i] {
			t.Fatal("comet appeared before idle threshold")
		}
	}
	c.idleComets(5.5)
	changed := 0
	for i := range before {
		if c.cells[i] != before[i] {
			changed++
			if before[i].ch != ' ' {
				t.Fatal("comet overwrote content")
			}
		}
	}
	if changed == 0 {
		t.Fatal("no comet appeared after idle threshold")
	}
}

func TestSkyMovesAndPanelRetainsStars(t *testing.T) {
	a, b := newCanvas(120, 36), newCanvas(120, 36)
	a.t, b.t = 0, 3
	a.backdrop()
	b.backdrop()
	changed := false
	for i := range a.cells {
		if a.cells[i] != b.cells[i] {
			changed = true
			break
		}
	}
	if !changed {
		t.Fatal("stars did not twinkle")
	}
	a.panel(2, 5, 110, 25, "", "", false, 0)
	stars := 0
	for _, p := range a.cells {
		if p.glass && (p.ch == '·' || p.ch == '.') {
			stars++
		}
	}
	if stars == 0 {
		t.Fatal("glass hid all stars")
	}
	if bgAt(45, 12, 0) == bgAt(45, 12, 3) && bgAt(85, 20, 0) == bgAt(85, 20, 3) {
		t.Fatal("aurora did not drift")
	}
}

func TestAnimationOffAndInteractionReset(t *testing.T) {
	a := newTestApp(t)
	a.cfg.Animations = false
	a.now = a.start.Add(time.Minute)
	if a.t() != 0 {
		t.Fatal("visual time must freeze when animations are off")
	}
	a.lastInput = time.Now().Add(-time.Minute)
	a.handle(Key{name: "right"})
	if time.Since(a.lastInput) > time.Second {
		t.Fatal("interaction did not reset idle timer")
	}
}

func TestStatusColorsDistinguishStoppedAndFailed(t *testing.T) {
	if stateColor("exited") == stateColor("dead") || stateColor("running") == stateColor("exited") {
		t.Fatal("running, intentionally stopped, and failed statuses must remain distinct")
	}
}

func TestAnimationOutputIsBounded(t *testing.T) {
	a := newTestApp(t)
	a.cfg.Animations = true
	a.tabStart = a.start.Add(-time.Hour)
	demoTelemetry(a)
	var output bytes.Buffer
	s := &screen{w: 150, h: 36, out: bufio.NewWriter(&output)}
	for i := 0; i < 26; i++ {
		a.now = a.start.Add(time.Duration(i) * 80 * time.Millisecond)
		c := newCanvas(150, 36)
		c.t = a.t()
		a.render(c)
		s.flush(c)
	}
	t.Logf("26 frames including initial full frame: %d bytes", output.Len())
	if output.Len() > 5_000_000 {
		t.Fatalf("excessive terminal output: %d bytes", output.Len())
	}
}
