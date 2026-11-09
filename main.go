package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
	"unicode/utf8"
	"unsafe"
)

type App struct {
	mu  sync.Mutex
	tmu sync.Mutex
	cfg Config
	tel Telemetry

	jobs    map[string]*Job
	running *Job

	sudoDocker   bool
	demo         bool
	forceRefresh chan struct{}
	wakeCh       chan struct{}
	quit         chan struct{}

	tab         int
	previousTab int
	sel         [16]int
	disp        map[string]float64
	tabX, tabW  float64
	tabStart    time.Time
	modal       *Modal
	hits        []hit
	start, now  time.Time
	lastInput   time.Time

	toastMsg string
	toastBad bool
	toastAt  time.Time
}

func (a *App) t() float64 {
	if !a.cfg.Animations {
		return 0
	}
	return a.now.Sub(a.start).Seconds()
}
func (a *App) wake() {
	select {
	case a.wakeCh <- struct{}{}:
	default:
	}
}
func (a *App) forceRefreshNow() {
	select {
	case a.forceRefresh <- struct{}{}:
	default:
	}
}
func (a *App) toast(msg string, bad bool) {
	a.tmu.Lock()
	a.toastMsg, a.toastBad, a.toastAt = msg, bad, time.Now()
	a.tmu.Unlock()
	a.wake()
}

// ───────────────────────── terminal ─────────────────────────

func ioctl(fd, req uintptr, arg unsafe.Pointer) error {
	if _, _, e := syscall.Syscall(syscall.SYS_IOCTL, fd, req, uintptr(arg)); e != 0 {
		return e
	}
	return nil
}

func makeRaw() (*syscall.Termios, error) {
	var old syscall.Termios
	if err := ioctl(0, syscall.TCGETS, unsafe.Pointer(&old)); err != nil {
		return nil, err
	}
	raw := old
	raw.Iflag &^= syscall.IGNBRK | syscall.BRKINT | syscall.PARMRK | syscall.ISTRIP | syscall.INLCR | syscall.IGNCR | syscall.ICRNL | syscall.IXON
	raw.Lflag &^= syscall.ECHO | syscall.ECHONL | syscall.ICANON | syscall.ISIG | syscall.IEXTEN
	raw.Cflag &^= syscall.CSIZE | syscall.PARENB
	raw.Cflag |= syscall.CS8
	raw.Cc[syscall.VMIN], raw.Cc[syscall.VTIME] = 1, 0
	if err := ioctl(0, syscall.TCSETS, unsafe.Pointer(&raw)); err != nil {
		return nil, err
	}
	return &old, nil
}

func termSize() (int, int) {
	var ws struct{ Row, Col, X, Y uint16 }
	if err := ioctl(1, syscall.TIOCGWINSZ, unsafe.Pointer(&ws)); err != nil || ws.Col == 0 {
		return 120, 36
	}
	return int(ws.Col), int(ws.Row)
}

// ───────────────────────── input ─────────────────────────

type Key struct {
	name string
	x, y int
}

func parseKeys(b []byte) []Key {
	var out []Key
	for len(b) > 0 {
		switch {
		case b[0] == 0x1b:
			if len(b) == 1 {
				out = append(out, Key{name: "esc"})
				return out
			}
			if b[1] == '[' || b[1] == 'O' {
				i := 2
				for i < len(b) && !(b[i] >= 0x40 && b[i] <= 0x7e) {
					i++
				}
				if i >= len(b) {
					return out
				}
				params, fin := string(b[2:i]), b[i]
				b = b[i+1:]
				switch {
				case strings.HasPrefix(params, "<"):
					p := strings.Split(params[1:], ";")
					if len(p) == 3 {
						bt, _ := strconv.Atoi(p[0])
						x, _ := strconv.Atoi(p[1])
						y, _ := strconv.Atoi(p[2])
						switch {
						case bt == 64:
							out = append(out, Key{"wheelup", x - 1, y - 1})
						case bt == 65:
							out = append(out, Key{"wheeldown", x - 1, y - 1})
						case bt == 0 && fin == 'M':
							out = append(out, Key{"click", x - 1, y - 1})
						}
					}
				case fin == 'A':
					out = append(out, Key{name: "up"})
				case fin == 'B':
					out = append(out, Key{name: "down"})
				case fin == 'C':
					out = append(out, Key{name: "right"})
				case fin == 'D':
					out = append(out, Key{name: "left"})
				case fin == 'H':
					out = append(out, Key{name: "home"})
				case fin == 'F':
					out = append(out, Key{name: "end"})
				case fin == 'Z':
					out = append(out, Key{name: "btab"})
				case fin == '~':
					switch params {
					case "5":
						out = append(out, Key{name: "pgup"})
					case "6":
						out = append(out, Key{name: "pgdn"})
					case "1", "7":
						out = append(out, Key{name: "home"})
					case "4", "8":
						out = append(out, Key{name: "end"})
					}
				}
				continue
			}
			out = append(out, Key{name: "esc"})
			b = b[1:]
		case b[0] == '\r' || b[0] == '\n':
			out = append(out, Key{name: "enter"})
			b = b[1:]
		case b[0] == '\t':
			out = append(out, Key{name: "tab"})
			b = b[1:]
		case b[0] == 3:
			out = append(out, Key{name: "ctrl-c"})
			b = b[1:]
		case b[0] == 4:
			out = append(out, Key{name: "pgdn"})
			b = b[1:]
		case b[0] == 21:
			out = append(out, Key{name: "pgup"})
			b = b[1:]
		case b[0] < 32:
			b = b[1:]
		default:
			r, n := utf8.DecodeRune(b)
			out = append(out, Key{name: string(r)})
			b = b[n:]
		}
	}
	return out
}

// ───────────────────────── key handling ─────────────────────────

func (a *App) setTab(i int) {
	n := len(tabs)
	i = ((i % n) + n) % n
	if i != a.tab {
		a.previousTab = a.tab
		a.tab = i
		a.tabStart = time.Now()
	}
}

func (a *App) currentJob() *Job {
	switch tabs[a.tab].Kind {
	case kOps:
		tb := tabs[a.tab]
		return a.jobs[tb.Name+"/"+tb.Ops[a.sel[a.tab]].ID]
	case kContainers:
		a.mu.Lock()
		defer a.mu.Unlock()
		if a.sel[a.tab] < len(a.tel.Containers) {
			return a.jobs["container/"+a.tel.Containers[a.sel[a.tab]].Name]
		}
	case kSettings:
		return a.jobs["settings/single"]
	}
	return nil
}

func (a *App) listLen() int {
	switch tabs[a.tab].Kind {
	case kOps:
		return len(tabs[a.tab].Ops)
	case kContainers:
		a.mu.Lock()
		defer a.mu.Unlock()
		return len(a.tel.Containers)
	case kSettings:
		return len(a.settingsList())
	}
	return 0
}

func (a *App) askQuit() {
	if a.running != nil {
		a.modal = &Modal{Title: "A task is still running", Confirm: true, Danger: true, Opened: time.Now(),
			Body:  []string{"Quit now? The running task will be asked to stop. Steps that cannot be interrupted safely (package installs, container recreation) finish first."},
			OnYes: func() { a.running.stopNow(); a.quit <- struct{}{} }}
		return
	}
	a.quit <- struct{}{}
}

func (a *App) activate() {
	if a.demo {
		a.toast("Demo is read-only; launch without --demo to run actions", false)
		return
	}
	tb := tabs[a.tab]
	switch tb.Kind {
	case kOps:
		op := tb.Ops[a.sel[a.tab]]
		run := func() { a.runOp(tb.Name, op) }
		if op.Confirm != "" {
			body := op.Confirm
			if op.Special != "" {
				a.mu.Lock()
				n := len(a.tel.Containers)
				a.mu.Unlock()
				body += fmt.Sprintf(" Pause between services: %ds. %d containers are known.", a.cfg.PullPause, n)
			}
			a.modal = &Modal{Title: op.Title, Body: []string{body}, Confirm: true, Danger: op.Danger, Opened: time.Now(), OnYes: run}
			return
		}
		run()
	case kContainers:
		a.mu.Lock()
		if a.sel[a.tab] >= len(a.tel.Containers) {
			a.mu.Unlock()
			return
		}
		ct := a.tel.Containers[a.sel[a.tab]]
		a.mu.Unlock()
		items := []string{"Inspect", "Logs", "Restart"}
		if ct.State == "running" {
			items = append(items, "Update this service")
			items = append([]string{"Stop"}, items...)
		} else {
			items = append([]string{"Start"}, items...)
		}
		a.modal = &Modal{Title: ct.Name, Items: items, Opened: time.Now(), OnPick: func(i int) {
			act := items[i]
			if act == "Stop" || act == "Restart" || act == "Update this service" {
				body := "Apply this to " + ct.Name + "?"
				if act == "Update this service" {
					body = "Pull and recreate this running single-container service. Avoid stopping it during the update."
				}
				a.modal = &Modal{Title: act + " " + ct.Name, Confirm: true, Opened: time.Now(),
					Body:  []string{body},
					OnYes: func() { a.containerAction(ct, act) }}
				return
			}
			a.containerAction(ct, act)
		}}
	case kSettings:
		switch a.sel[a.tab] {
		case 0:
			a.cfg.Animations = !a.cfg.Animations
		case 1:
			a.cfg.RefreshSecond = map[int]int{2: 5, 5: 10, 10: 2}[a.cfg.RefreshSecond]
			a.forceRefreshNow()
		case 2:
			a.cfg.PullPause = map[int]int{0: 5, 5: 8, 8: 15, 15: 30, 30: 0}[a.cfg.PullPause]
			if a.cfg.PullPause == 0 && a.cfg.PullPause != 0 {
				a.cfg.PullPause = 8
			}
		case 3:
			a.modal = &Modal{Title: "Single-layer pulls", Confirm: true, Opened: time.Now(),
				Body:  []string{"Set Docker's concurrent uploads and downloads to 1, save a backup if none exists, and reload Docker. Reload behavior depends on your Docker setup."},
				OnYes: func() { a.startJob("settings/single", "Single-layer pulls", a.enableSingleLayer) }}
			return
		case 4:
			a.modal = a.helpModal()
			return
		}
		if err := saveConfig(a.cfg); err != nil {
			a.toast("Could not save settings: "+err.Error(), true)
		} else {
			a.toast("Saved", false)
		}
	}
}

func (a *App) handle(k Key) {
	a.lastInput = time.Now()
	if a.modal != nil {
		m := a.modal
		switch k.name {
		case "esc", "n", "q", "?":
			a.modal = nil
		case "up", "k":
			if m.Idx > 0 {
				m.Idx--
			}
		case "down", "j":
			if m.Idx < len(m.Items)-1 {
				m.Idx++
			}
		case "enter", "y":
			a.modal = nil
			if m.OnPick != nil {
				m.OnPick(m.Idx)
			} else if m.OnYes != nil {
				m.OnYes()
			}
		case "click":
			for _, h := range a.hits {
				if h.kind == "modal" && k.x >= h.x && k.x < h.x+h.w && k.y == h.y {
					a.modal = nil
					if m.OnPick != nil {
						m.OnPick(h.idx)
					}
					return
				}
			}
		}
		return
	}
	scroll := func(d int) {
		if j := a.currentJob(); j != nil {
			j.set(func() { j.Scroll = max(0, j.Scroll+d) })
		}
	}
	move := func(d int) {
		n := a.listLen()
		if n == 0 {
			return
		}
		a.sel[a.tab] = max(0, min(n-1, a.sel[a.tab]+d))
	}
	switch k.name {
	case "q", "ctrl-c":
		a.askQuit()
	case "right", "tab":
		a.setTab(a.tab + 1)
	case "left", "btab":
		a.setTab(a.tab - 1)
	case "up", "k":
		move(-1)
	case "down", "j":
		move(1)
	case "enter", " ":
		a.activate()
	case "pgup":
		scroll(8)
	case "pgdn":
		scroll(-8)
	case "g", "home":
		scroll(1 << 20)
	case "G", "end":
		scroll(-(1 << 20))
	case "r":
		a.forceRefreshNow()
		a.toast("Refreshing", false)
	case "x":
		if a.running != nil {
			a.running.stopNow()
			a.toast("Stopping after the current safe point", false)
		}
	case "?":
		a.modal = a.helpModal()
	case "wheelup", "wheeldown":
		d := 3
		if k.name == "wheeldown" {
			d = -3
		}
		over := false
		for _, h := range a.hits {
			if h.kind == "out" && k.x >= h.x && k.x < h.x+h.w && k.y >= h.y && k.y < h.y+h.h {
				over = true
			}
		}
		if over {
			scroll(d)
		} else {
			move(-d / 3)
		}
	case "click":
		for _, h := range a.hits {
			if k.x >= h.x && k.x < h.x+h.w && k.y >= h.y && k.y < h.y+h.h {
				switch h.kind {
				case "tab":
					a.setTab(h.idx)
				case "item":
					a.sel[a.tab] = h.idx
				}
				return
			}
		}
	default:
		if len(k.name) == 1 && k.name[0] >= '0' && k.name[0] <= '9' {
			n := int(k.name[0] - '0')
			if n == 0 {
				n = 10
			}
			if n <= len(tabs) {
				a.setTab(n - 1)
			}
		}
	}
}

// ───────────────────────── main ─────────────────────────

func main() {
	a := &App{
		cfg: loadConfig(), jobs: map[string]*Job{}, disp: map[string]float64{},
		forceRefresh: make(chan struct{}, 1), wakeCh: make(chan struct{}, 1), quit: make(chan struct{}, 4),
		start: time.Now(), now: time.Now(), lastInput: time.Now(), tabStart: time.Now().Add(-time.Hour),
	}
	args := os.Args[1:]
	demo, wantSudo, snap := false, false, ""
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--demo":
			demo = true
		case "--sudo":
			wantSudo = true
		case "--snapshot":
			if i+1 < len(args) {
				snap = args[i+1]
				i++
			}
		case "-v", "--version":
			fmt.Println("homelab", version)
			return
		case "-h", "--help":
			fmt.Println("homelab " + version + "\n\nUsage: homelab [--demo] [--sudo]\n\n  --demo  Show fictional data; actions are disabled.\n  --sudo  Ask for sudo before entering the dashboard.\n\nConfig: " + configPath())
			return
		}
	}
	a.demo = demo || snap != ""
	a.initStatic()
	if demo || snap != "" {
		demoTelemetry(a)
	}
	if snap != "" {
		a.snapshot(snap, args)
		return
	}
	fi, _ := os.Stdin.Stat()
	if fi == nil || fi.Mode()&os.ModeCharDevice == 0 {
		fmt.Println("homelab needs an interactive terminal.")
		os.Exit(1)
	}
	// Ask for elevated access only when explicitly requested, never in demo.
	if wantSudo && !demo && os.Geteuid() != 0 {
		fmt.Println("Requesting sudo for privileged actions.")
		cmd := exec.Command("sudo", "-v")
		cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
		if err := cmd.Run(); err != nil {
			fmt.Fprintln(os.Stderr, "Sudo authorization unavailable; privileged actions may fail.")
		} else {
			go func() {
				for range time.Tick(3 * time.Minute) {
					_ = exec.Command("sudo", "-n", "-v").Run()
				}
			}()
		}
	}
	if !demo && exec.Command("docker", "info").Run() != nil && os.Geteuid() != 0 {
		if exec.Command("sudo", "-n", "docker", "info").Run() == nil {
			a.sudoDocker = true
		}
	}
	old, err := makeRaw()
	if err != nil {
		fmt.Println("cannot take over the terminal:", err)
		os.Exit(1)
	}
	out := bufio.NewWriterSize(os.Stdout, 1<<16)
	out.WriteString("\x1b[?1049h\x1b[?25l\x1b[?1000h\x1b[?1006h")
	out.Flush()
	restore := func() {
		out.WriteString("\x1b[?1006l\x1b[?1000l\x1b[?25h\x1b[?1049l\x1b[0m")
		out.Flush()
		_ = ioctl(0, syscall.TCSETS, unsafe.Pointer(old))
	}
	defer restore()
	defer func() {
		if r := recover(); r != nil {
			restore()
			fmt.Println("homelab crashed:", r)
			os.Exit(2)
		}
	}()

	if !demo {
		go a.collectFast()
		go a.collectSlow()
	}
	keys := make(chan Key, 64)
	go func() {
		buf := make([]byte, 256)
		for {
			n, err := os.Stdin.Read(buf)
			if err != nil {
				return
			}
			for _, k := range parseKeys(buf[:n]) {
				keys <- k
			}
		}
	}()
	sig := make(chan os.Signal, 4)
	signal.Notify(sig, syscall.SIGWINCH, syscall.SIGTERM, syscall.SIGHUP)

	w, h := termSize()
	scr := &screen{w: w, h: h, out: out}
	frame := func() {
		a.now = time.Now()
		cv := newCanvas(w, h)
		cv.t = a.t()
		if !a.cfg.Animations {
			cv.t = 0
		}
		a.render(cv)
		scr.flush(cv)
	}
	interval := func() time.Duration {
		if a.cfg.Animations {
			return 80 * time.Millisecond
		}
		return 400 * time.Millisecond
	}
	tk := time.NewTimer(interval())
	frame()
	for {
		select {
		case k := <-keys:
			a.handle(k)
			frame()
		case <-tk.C:
			frame()
			tk.Reset(interval())
		case <-a.wakeCh:
			if !a.cfg.Animations {
				frame()
			}
		case s := <-sig:
			if s == syscall.SIGWINCH {
				w, h = termSize()
				scr.invalidate()
				continue
			}
			return
		case <-a.quit:
			return
		}
	}
}

// snapshot: dump one frame as JSON for visual testing
func (a *App) snapshot(spec string, args []string) {
	var w, h int
	fmt.Sscanf(spec, "%dx%d", &w, &h)
	for i, v := range args {
		if v == "--tab" && i+1 < len(args) {
			a.tab, _ = strconv.Atoi(args[i+1])
		}
		if v == "--sel" && i+1 < len(args) {
			a.sel[a.tab], _ = strconv.Atoi(args[i+1])
		}
		if v == "--modal" {
			a.modal = &Modal{Title: "Update everything", Confirm: true, Opened: time.Now().Add(-time.Hour),
				Body: []string{"Upgrade Ubuntu, then update every container individually. Pause between services: 8s."}}
		}
		if v == "--job" {
			j := &Job{Title: "Update Docker stack", Running: true, Start: time.Now().Add(-42 * time.Second), Step: 3, Steps: 10, StepLabel: "app", Note: "resting 5s before the next service"}
			for _, l := range []string{"Docker is limited to one layer download at a time.", "Updating 9 services, strictly one after another.", "", "▸ app  (1/9)", "  pulling image…", "  recreating if the image changed…", "  ✓ app is current", "", "▸ worker  (2/9)", "  pulling image…", "  ✗ pull failed — container left untouched"} {
				j.Lines = append(j.Lines, l)
			}
			a.jobs[tabs[a.tab].Name+"/"+tabs[a.tab].Ops[a.sel[a.tab]].ID] = j
		}
	}
	a.start = time.Now().Add(-37 * time.Second)
	a.now = time.Now()
	a.tabStart = a.now.Add(-time.Hour)
	cv := newCanvas(w, h)
	cv.t = 37
	for i := 0; i < 40; i++ { // let eased values settle
		a.hits = a.hits[:0]
		cv = newCanvas(w, h)
		cv.t = 37
		a.render(cv)
	}
	type row [][4]any
	var rows []row
	for y := 0; y < h; y++ {
		var r row
		for x := 0; x < w; x++ {
			c := cv.cells[y*w+x]
			r = append(r, [4]any{string(c.ch), fmt.Sprintf("%02x%02x%02x", c.fg.r, c.fg.g, c.fg.b), fmt.Sprintf("%02x%02x%02x", c.bg.r, c.bg.g, c.bg.b), c.bold})
		}
		rows = append(rows, r)
	}
	json.NewEncoder(os.Stdout).Encode(rows)
}
