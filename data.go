package main

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"time"
)

// ───────────────────────── config ─────────────────────────

type Config struct {
	ArrDir        string `json:"arr_dir"`
	MediaDir      string `json:"media_dir"`
	BackupDir     string `json:"backup_dir"`
	ReportDir     string `json:"report_dir"`
	ComposeFile   string `json:"compose_file"`
	Animations    bool   `json:"animations"`
	RefreshSecond int    `json:"refresh_seconds"`
	PullPause     int    `json:"pull_pause_seconds"`
}

func exists(p string) bool { _, err := os.Stat(p); return err == nil }

func firstExisting(def string, paths ...string) string {
	for _, p := range paths {
		if info, err := os.Stat(p); err == nil && info.IsDir() {
			return p
		}
	}
	return def
}

// discoverCompose checks only conventional locations; it never scans the disk.
// More than one candidate in the same directory is ambiguous and fails closed.
func discoverCompose(home, cwd string) string {
	if path := os.Getenv("HOMELAB_COMPOSE_FILE"); path != "" {
		absolute, err := filepath.Abs(path)
		if err == nil {
			return absolute
		}
		return path
	}
	for _, dir := range []string{cwd, filepath.Join(home, "arr"), filepath.Join(home, "homelab"), "/opt/arr"} {
		if dir == "" {
			continue
		}
		var found []string
		for _, name := range []string{"compose.yaml", "compose.yml", "docker-compose.yml", "docker-compose.yaml"} {
			p := filepath.Join(dir, name)
			if info, err := os.Stat(p); err == nil && info.Mode().IsRegular() {
				found = append(found, p)
			}
		}
		if len(found) == 1 {
			p, err := filepath.Abs(found[0])
			if err == nil {
				return p
			}
			return found[0]
		}
		if len(found) > 1 {
			return ""
		}
	}
	return ""
}

func defaultConfig() Config {
	home, _ := os.UserHomeDir()
	cwd, _ := os.Getwd()
	compose := discoverCompose(home, cwd)
	arr := filepath.Join(home, "homelab")
	if compose != "" {
		arr = filepath.Dir(compose)
	}
	media := firstExisting(filepath.Join(home, "media"), filepath.Join(home, "media"), "/mnt/media", "/srv/media", filepath.Join(arr, "media"))
	return Config{
		ArrDir: arr, MediaDir: media, ComposeFile: compose,
		BackupDir: filepath.Join(arr, "backups"), ReportDir: filepath.Join(arr, "reports"),
		Animations: true, RefreshSecond: 5, PullPause: 8,
	}
}

func configPath() string {
	d, err := os.UserConfigDir()
	if err != nil {
		home, _ := os.UserHomeDir()
		d = filepath.Join(home, ".config")
	}
	return filepath.Join(d, "homelab", "config.json")
}

func loadConfig() Config {
	cfg := defaultConfig()
	if b, err := os.ReadFile(configPath()); err == nil {
		candidate := cfg
		var keys map[string]json.RawMessage
		if err := json.Unmarshal(b, &candidate); err == nil && json.Unmarshal(b, &keys) == nil && keys != nil {
			// JSON null does not change a Go string during unmarshal; ensure an
			// explicit null never falls back to an automatically detected stack.
			if raw, present := keys["compose_file"]; present && strings.TrimSpace(string(raw)) == "null" {
				candidate.ComposeFile = ""
			}
			// Derive sibling paths from an explicitly configured Compose file,
			// unless the user supplied their own paths.
			if _, explicit := keys["compose_file"]; explicit && candidate.ComposeFile != "" {
				if p, e := filepath.Abs(candidate.ComposeFile); e == nil {
					candidate.ComposeFile = p
				}
				if _, present := keys["arr_dir"]; !present {
					candidate.ArrDir = filepath.Dir(candidate.ComposeFile)
				}
				if _, present := keys["backup_dir"]; !present {
					candidate.BackupDir = filepath.Join(candidate.ArrDir, "backups")
				}
				if _, present := keys["report_dir"]; !present {
					candidate.ReportDir = filepath.Join(candidate.ArrDir, "reports")
				}
			}
			cfg = candidate
		} else {
			cfg.ComposeFile = ""
		}
	} else if !os.IsNotExist(err) {
		// An unreadable or invalid config path must not select another stack.
		cfg.ComposeFile = ""
	}
	if cfg.RefreshSecond != 2 && cfg.RefreshSecond != 5 && cfg.RefreshSecond != 10 {
		cfg.RefreshSecond = 5
	}
	if cfg.PullPause < 0 || cfg.PullPause > 120 {
		cfg.PullPause = 8
	}
	return cfg
}

func saveConfig(cfg Config) error {
	p := configPath()
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return err
	}
	b, _ := json.MarshalIndent(cfg, "", "  ")
	return os.WriteFile(p, b, 0o644)
}

// ───────────────────────── telemetry ─────────────────────────

type Container struct {
	Name, Image, State, Status, Ports, Service string
}

type Telemetry struct {
	CPU, MemPct, SwapPct  float64
	MemUsed, MemTotal     float64 // GiB
	Load                  [3]float64
	Temp                  float64
	Uptime                time.Duration
	RxRate, TxRate        float64 // bytes/s
	CPUHist, MemHist      []float64
	RxHist, TxHist        []float64
	RootPct, MediaPct     float64
	RootUsed, RootTotal   float64
	MediaUsed, MediaTotal float64
	Containers            []Container
	DockerOK              bool
	DockerErr             string
	Hostname, IP, Kernel  string
	Cores                 int
	SingleLayer           bool
	LastDocker            time.Time
}

type cpuSample struct{ idle, total uint64 }

func readCPU() cpuSample {
	f, err := os.Open("/proc/stat")
	if err != nil {
		return cpuSample{}
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	if sc.Scan() {
		fs := strings.Fields(sc.Text())
		var total, idle uint64
		for i, v := range fs[1:] {
			n, _ := strconv.ParseUint(v, 10, 64)
			total += n
			if i == 3 || i == 4 {
				idle += n
			}
		}
		return cpuSample{idle, total}
	}
	return cpuSample{}
}

func readMem() (used, total, swapPct, avail float64) {
	f, err := os.Open("/proc/meminfo")
	if err != nil {
		return
	}
	defer f.Close()
	m := map[string]float64{}
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		fs := strings.Fields(sc.Text())
		if len(fs) >= 2 {
			v, _ := strconv.ParseFloat(fs[1], 64)
			m[strings.TrimSuffix(fs[0], ":")] = v
		}
	}
	total = m["MemTotal"] / 1048576
	avail = m["MemAvailable"] / 1048576
	used = total - avail
	if m["SwapTotal"] > 0 {
		swapPct = (m["SwapTotal"] - m["SwapFree"]) / m["SwapTotal"] * 100
	}
	return
}

func readNet() (rx, tx float64) {
	f, err := os.Open("/proc/net/dev")
	if err != nil {
		return
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := sc.Text()
		i := strings.Index(line, ":")
		if i < 0 {
			continue
		}
		name := strings.TrimSpace(line[:i])
		if name == "lo" || strings.HasPrefix(name, "veth") || strings.HasPrefix(name, "docker") || strings.HasPrefix(name, "br-") {
			continue
		}
		fs := strings.Fields(line[i+1:])
		if len(fs) >= 9 {
			a, _ := strconv.ParseFloat(fs[0], 64)
			b, _ := strconv.ParseFloat(fs[8], 64)
			rx += a
			tx += b
		}
	}
	return
}

func readLoad() [3]float64 {
	var l [3]float64
	b, err := os.ReadFile("/proc/loadavg")
	if err == nil {
		fs := strings.Fields(string(b))
		for i := 0; i < 3 && i < len(fs); i++ {
			l[i], _ = strconv.ParseFloat(fs[i], 64)
		}
	}
	return l
}

func readTemp() float64 {
	best := 0.0
	m, _ := filepath.Glob("/sys/class/thermal/thermal_zone*/temp")
	for _, p := range m {
		b, err := os.ReadFile(p)
		if err != nil {
			continue
		}
		v, _ := strconv.ParseFloat(strings.TrimSpace(string(b)), 64)
		v /= 1000
		if v > 1 && v < 125 && v > best {
			best = v
		}
	}
	return best
}

func readUptime() time.Duration {
	b, err := os.ReadFile("/proc/uptime")
	if err != nil {
		return 0
	}
	fs := strings.Fields(string(b))
	if len(fs) == 0 {
		return 0
	}
	v, _ := strconv.ParseFloat(fs[0], 64)
	return time.Duration(v * float64(time.Second))
}

func diskUse(path string) (pct, used, total float64) {
	var st syscall.Statfs_t
	if err := syscall.Statfs(path, &st); err != nil {
		return
	}
	t := float64(st.Blocks) * float64(st.Bsize)
	fr := float64(st.Bavail) * float64(st.Bsize)
	if t <= 0 {
		return
	}
	return (t - fr) / t * 100, (t - fr) / (1 << 30), t / (1 << 30)
}

func singleLayerPulls() bool {
	b, err := os.ReadFile("/etc/docker/daemon.json")
	if err != nil {
		return false
	}
	var m map[string]any
	if json.Unmarshal(b, &m) != nil {
		return false
	}
	v, ok := m["max-concurrent-downloads"].(float64)
	return ok && v == 1
}

func (a *App) dockerCmd(args ...string) *exec.Cmd {
	if a.sudoDocker {
		return exec.Command("sudo", append([]string{"-n", "docker"}, args...)...)
	}
	return exec.Command("docker", args...)
}

func (a *App) readContainers() ([]Container, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	cmd := a.dockerCmd("ps", "-a", "--format", "{{.Names}}\t{{.Image}}\t{{.State}}\t{{.Status}}\t{{.Ports}}\t{{.Label \"com.docker.compose.service\"}}")
	cmd2 := exec.CommandContext(ctx, cmd.Path, cmd.Args[1:]...)
	out, err := cmd2.Output()
	if err != nil {
		return nil, err
	}
	var list []Container
	for _, ln := range strings.Split(strings.TrimRight(string(out), "\r\n"), "\n") {
		if ln == "" {
			continue
		}
		p := strings.Split(strings.TrimSuffix(ln, "\r"), "\t")
		for len(p) < 6 {
			p = append(p, "")
		}
		list = append(list, Container{p[0], p[1], p[2], p[3], p[4], p[5]})
	}
	sort.Slice(list, func(i, j int) bool { return list[i].Name < list[j].Name })
	return list, nil
}

func push(h []float64, v float64) []float64 {
	h = append(h, v)
	if len(h) > 240 {
		h = h[len(h)-240:]
	}
	return h
}

// fast collector: 1 Hz, very light
func (a *App) collectFast() {
	prevCPU := readCPU()
	prevRx, prevTx := readNet()
	last := time.Now()
	tk := time.NewTicker(time.Second)
	for range tk.C {
		now := time.Now()
		dt := now.Sub(last).Seconds()
		last = now
		cs := readCPU()
		cpu := 0.0
		if d := cs.total - prevCPU.total; d > 0 {
			cpu = float64(d-(cs.idle-prevCPU.idle)) / float64(d) * 100
		}
		prevCPU = cs
		rx, tx := readNet()
		rr, tr := (rx-prevRx)/dt, (tx-prevTx)/dt
		if rr < 0 {
			rr = 0
		}
		if tr < 0 {
			tr = 0
		}
		prevRx, prevTx = rx, tx
		used, total, swap, _ := readMem()
		a.mu.Lock()
		t := &a.tel
		t.CPU, t.RxRate, t.TxRate = cpu, rr, tr
		t.MemUsed, t.MemTotal, t.SwapPct = used, total, swap
		if total > 0 {
			t.MemPct = used / total * 100
		}
		t.Load, t.Temp, t.Uptime = readLoad(), readTemp(), readUptime()
		t.CPUHist, t.MemHist = push(t.CPUHist, cpu), push(t.MemHist, t.MemPct)
		t.RxHist, t.TxHist = push(t.RxHist, rr), push(t.TxHist, tr)
		a.mu.Unlock()
		a.wake()
	}
}

// slow collector: docker + disks
func (a *App) collectSlow() {
	for {
		list, err := a.readContainers()
		rp, ru, rt := diskUse("/")
		mp, mu, mt := diskUse(a.cfg.MediaDir)
		sl := singleLayerPulls()
		a.mu.Lock()
		t := &a.tel
		if err == nil {
			t.Containers, t.DockerOK, t.DockerErr = list, true, ""
		} else {
			t.DockerOK, t.DockerErr = false, err.Error()
		}
		t.LastDocker = time.Now()
		t.RootPct, t.RootUsed, t.RootTotal = rp, ru, rt
		t.MediaPct, t.MediaUsed, t.MediaTotal = mp, mu, mt
		t.SingleLayer = sl
		a.mu.Unlock()
		a.wake()
		select {
		case <-time.After(time.Duration(a.cfg.RefreshSecond) * time.Second):
		case <-a.forceRefresh:
		}
	}
}

func (a *App) initStatic() {
	h, _ := os.Hostname()
	a.tel.Hostname = h
	a.tel.Cores = runtime.NumCPU()
	if out, err := exec.Command("sh", "-c", "hostname -I 2>/dev/null | awk '{print $1}'").Output(); err == nil {
		a.tel.IP = strings.TrimSpace(string(out))
	}
	if b, err := os.ReadFile("/proc/sys/kernel/osrelease"); err == nil {
		a.tel.Kernel = strings.TrimSpace(string(b))
	}
}

// ───────────────────────── formatting ─────────────────────────

func humanRate(b float64) string {
	switch {
	case b >= 1<<30:
		return fmt.Sprintf("%.1f GB/s", b/(1<<30))
	case b >= 1<<20:
		return fmt.Sprintf("%.1f MB/s", b/(1<<20))
	case b >= 1<<10:
		return fmt.Sprintf("%.0f KB/s", b/(1<<10))
	}
	return fmt.Sprintf("%.0f B/s", b)
}

func humanUp(d time.Duration) string {
	if d <= 0 {
		return "—"
	}
	days := int(d.Hours()) / 24
	h := int(d.Hours()) % 24
	m := int(d.Minutes()) % 60
	switch {
	case days > 0:
		return fmt.Sprintf("%dd %dh %dm", days, h, m)
	case h > 0:
		return fmt.Sprintf("%dh %dm", h, m)
	}
	return fmt.Sprintf("%dm", m)
}

func demoTelemetry(a *App) {
	a.tel.Hostname, a.tel.IP, a.tel.Kernel, a.tel.Cores = "demo-host", "192.0.2.10", "6.8.0", 4
	a.tel.DockerOK = true
	a.tel.Containers = []Container{
		{"media", "example/media:demo", "running", "Up 3 days", "8080/tcp", "media"},
		{"dashboard", "example/dashboard:demo", "running", "Up 3 days", "3000/tcp", "dashboard"},
		{"photos", "example/photos:demo", "running", "Up 2 days", "8000/tcp", "photos"},
		{"backup", "example/backup:demo", "exited", "Exited (0) 2 hours ago", "", "backup"},
	}
	for i := 0; i < 120; i++ {
		x := float64(i)
		a.tel.CPUHist = append(a.tel.CPUHist, 25+20*sinf(x/7)+10*sinf(x/2.3))
		a.tel.MemHist = append(a.tel.MemHist, 48+6*sinf(x/20))
		a.tel.RxHist = append(a.tel.RxHist, (3+3*sinf(x/9)+1.5*sinf(x/3))*1e6)
		a.tel.TxHist = append(a.tel.TxHist, (1+1*sinf(x/11))*1e6)
	}
	a.tel.CPU, a.tel.MemPct, a.tel.MemUsed, a.tel.MemTotal = 37, 52, 4.1, 7.8
	a.tel.Load = [3]float64{0.9, 1.1, 0.8}
	a.tel.Temp, a.tel.Uptime = 54, 74*time.Hour+22*time.Minute
	a.tel.RxRate, a.tel.TxRate = 4.2e6, 1.1e6
	a.tel.RootPct, a.tel.RootUsed, a.tel.RootTotal = 41, 98, 238
	a.tel.MediaPct, a.tel.MediaUsed, a.tel.MediaTotal = 78, 3100, 3970
}
