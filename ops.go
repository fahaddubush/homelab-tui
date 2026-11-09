package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"os/exec"
	"regexp"
	"strings"
	"sync"
	"syscall"
	"time"
)

func sinf(x float64) float64 { return math.Sin(x) }

// ───────────────────────── operations ─────────────────────────

type Op struct {
	ID, Title, Desc string
	Cmd             string
	Confirm         string // non-empty → ask first
	Danger          bool
	Special         string
}

type Tab struct {
	Name string
	Kind int
	Ops  []Op
}

// Exclude the configured backup directory, including a custom nested path.
const backupSetup = `test -d "$HL_ARR" || { echo 'Source directory not found'; exit 1; }; R=$(realpath "$HL_ARR") || exit 1; D=$(realpath -m "$HL_BACKUP") || exit 1; [ "$R" != / ] && [ "$R" != "$D" ] || { echo 'Invalid source or backup directory'; exit 1; }; B=$(basename "$R"); case "$D/" in "$R"/*) X="$B/${D#"$R"/}";; *) X='__homelab_no_nested_backup__';; esac; mkdir -p "$D" || exit 1; `

const (
	kOverview = iota
	kContainers
	kOps
	kSettings
)

var tabs = []Tab{
	{Name: "Overview", Kind: kOverview},
	{Name: "Containers", Kind: kContainers},
	{Name: "Updates", Kind: kOps, Ops: []Op{
		{ID: "check", Title: "Check for updates", Desc: "Cached APT upgrade list and Compose validity",
			Cmd: `echo '── Cached package list ──'; if command -v apt >/dev/null; then apt list --upgradable 2>/dev/null | sed '1d' | sed 's/^/  /'; else echo '  APT unavailable on this system'; fi; echo; echo '── Compose ──'; dc config --quiet && echo "  valid: $HL_COMPOSE"; echo; echo '── Services ──'; dc config --services | sed 's/^/  /'`},
		{ID: "stack", Title: "Update Docker stack", Desc: "Update running single-container services", Special: "update-stack",
			Confirm: "Pull and recreate running single-container services one by one. Stopped and scaled services are skipped. Avoid stopping services while updating."},
		{ID: "os", Title: "Update Ubuntu", Desc: "Packages only, low priority", Special: "update-os",
			Confirm: "Run a full APT upgrade. It can remove packages and restart system services, including Docker. Review package changes outside HomeLab first."},
		{ID: "all", Title: "Update everything", Desc: "Ubuntu first, then running containers", Special: "update-all",
			Confirm: "Upgrade Ubuntu, then update running single-container services. The OS upgrade may restart Docker and start previously stopped containers. Stopped and scaled services are skipped by the container updater. Avoid stopping services during the update."},
		{ID: "validate", Title: "Validate Compose file", Desc: "Syntax and variable check",
			Cmd: `dc config --quiet && echo "Compose file is valid." && echo "  $HL_COMPOSE"`},
	}},
	{Name: "Storage", Kind: kOps, Ops: []Op{
		{ID: "fs", Title: "Filesystem overview", Desc: "Disks, mount points, free space",
			Cmd: `lsblk -o NAME,SIZE,FSTYPE,FSAVAIL,FSUSE%,MOUNTPOINTS,MODEL; echo; df -h -x tmpfs -x devtmpfs`},
		{ID: "media", Title: "Media folder sizes", Desc: "Size of every top-level media folder",
			Cmd: `$NI du -h --max-depth=1 "$HL_MEDIA" 2>/dev/null | sort -h`},
		{ID: "largest", Title: "Largest media files", Desc: "The thirty heaviest files",
			Cmd: `$NI find "$HL_MEDIA" -type f -printf '%s\t%p\n' 2>/dev/null | sort -nr | head -30 | numfmt --field=1 --to=iec`},
		{ID: "dockerdf", Title: "Docker disk usage", Desc: "Images, layers, volumes, build cache",
			Cmd: `$D system df -v`},
		{ID: "smart", Title: "SMART disk health", Desc: "Drive health and attributes",
			Cmd: `for d in /dev/sd? /dev/nvme?n?; do [ -e "$d" ] || continue; echo; echo "=== $d ==="; $S smartctl -H -A "$d"; done`},
	}},
	{Name: "Network", Kind: kOps, Ops: []Op{
		{ID: "ifaces", Title: "Interfaces and routes", Desc: "Addresses and routing table", Cmd: `ip -brief address; echo; ip route`},
		{ID: "ports", Title: "Listening ports", Desc: "Processes accepting connections", Cmd: `$S ss -tulpn`},
		{ID: "dnets", Title: "Docker networks", Desc: "Bridge and container networking", Cmd: `$D network ls; echo; $D network inspect bridge 2>/dev/null`},
		{ID: "conn", Title: "Connectivity test", Desc: "Gateway, internet and DNS",
			Cmd: `echo 'Gateway:'; ping -c 3 "$(ip route | awk '/default/{print $3;exit}')"; echo; echo 'Internet:'; ping -c 3 1.1.1.1; echo; echo 'DNS:'; getent hosts ubuntu.com`},
		{ID: "addr", Title: "Published container ports", Desc: "Actual Docker bindings for running containers",
			Cmd: `echo 'Published container ports (not necessarily HTTP URLs):'; $D ps --format '{{.Names}}\t{{.Ports}}'`},
	}},
	{Name: "Security", Kind: kOps, Ops: []Op{
		{ID: "ufw", Title: "Firewall status", Desc: "UFW rules and state", Cmd: `$S ufw status verbose 2>/dev/null || echo 'UFW is not installed.'`},
		{ID: "f2b", Title: "Fail2Ban", Desc: "Daemon and SSH jail",
			Cmd: `command -v fail2ban-client >/dev/null && $S fail2ban-client status && $S fail2ban-client status sshd || echo 'Fail2Ban is not installed or the sshd jail is inactive.'`},
		{ID: "crowdsec", Title: "CrowdSec", Desc: "Local metrics and decisions",
			Cmd: `command -v cscli >/dev/null && $S cscli metrics && $S cscli decisions list || echo 'Local CrowdSec is not installed.'`},
		{ID: "ssh", Title: "SSH audit", Desc: "Effective settings and service state",
			Cmd: `echo 'Effective SSH settings:'; $S sshd -T | grep -E 'passwordauthentication|pubkeyauthentication|kbdinteractiveauthentication|permitrootlogin|maxauthtries|allowusers'; echo; echo 'Service:'; systemctl status ssh --no-pager -n 8`},
		{ID: "auth", Title: "Authentication events", Desc: "SSH logins in the last day",
			Cmd: `$S journalctl -u ssh --since '24 hours ago' --no-pager | grep -iE 'accepted|failed|invalid|authentication' | tail -100`},
		{ID: "perms", Title: "Path and permissions", Desc: "Ownership of core paths and SSH keys",
			Cmd: `for p in "$HL_ARR" "$HL_MEDIA" "$HL_BACKUP"; do echo; namei -l "$p"; done; echo; namei -l "$HOME/.ssh/authorized_keys"; echo; stat -c '%A %a %U:%G %n' "$HOME/.ssh" "$HOME/.ssh/authorized_keys"`},
	}},
	{Name: "Backups", Kind: kOps, Ops: []Op{
		{ID: "essential", Title: "Essential backup", Desc: "Configs without caches and logs",
			Cmd: backupSetup + `f="$D/homelab-essential_$(date +%Y-%m-%d_%H-%M-%S).tar.gz"; $NI tar --exclude="$X" --exclude="$B/logs" --exclude='*/Cache' --exclude='*/Logs' -czf "$f" -C "$(dirname "$R")" "$B" && ls -lh "$f"`},
		{ID: "full", Title: "Full configuration backup", Desc: "Everything except backups and caches",
			Cmd: backupSetup + `f="$D/homelab-full_$(date +%Y-%m-%d_%H-%M-%S).tar.gz"; $NI tar --exclude="$X" -czf "$f" -C "$(dirname "$R")" "$B" && ls -lh "$f"`},
		{ID: "list", Title: "List backups", Desc: "Newest archives first", Cmd: `ls -lht "$HL_BACKUP"/*.tar.gz 2>/dev/null || echo 'No backups yet.'`},
		{ID: "verify", Title: "Verify latest backup", Desc: "Test gzip integrity and contents",
			Cmd: `f=$(ls -t "$HL_BACKUP"/*.tar.gz 2>/dev/null | head -1); [ -n "$f" ] && gzip -t "$f" && tar -tzf "$f" >/dev/null && echo "VERIFIED: $f" || echo 'No valid backup found.'`},
		{ID: "prune", Title: "Delete old backups", Desc: "HomeLab archives older than 30 days", Danger: true,
			Confirm: "Permanently delete HomeLab-created backup archives older than 30 days from the backup directory.",
			Cmd:     `find "$HL_BACKUP" -maxdepth 1 -type f \( -name 'homelab-essential_*.tar.gz' -o -name 'homelab-full_*.tar.gz' \) -mtime +30 -print -delete; echo; echo 'Done.'`},
	}},
	{Name: "Logs", Kind: kOps, Ops: []Op{
		{ID: "stack", Title: "Stack logs", Desc: "Latest lines from every service", Cmd: `dc logs --no-color --tail 40 2>&1`},
		{ID: "journal", Title: "System journal", Desc: "Last two hours of activity", Cmd: `$S journalctl --since '2 hours ago' --no-pager | tail -250`},
		{ID: "kernel", Title: "Kernel warnings", Desc: "Hardware and driver warnings", Cmd: `$S journalctl -k -p warning --since '24 hours ago' --no-pager | tail -250`},
		{ID: "failed", Title: "Failed services", Desc: "Failed units and journal errors",
			Cmd: `systemctl --failed --no-pager; echo; $S journalctl -p err --since '24 hours ago' --no-pager | tail -250`},
	}},
	{Name: "Maintenance", Kind: kOps, Ops: []Op{
		{ID: "info", Title: "System information", Desc: "Kernel, Docker and OS versions",
			Cmd: `echo "homelab $HL_VERSION"; echo; uname -a; echo; $D --version; $D compose version; echo; cat /etc/os-release`},
		{ID: "report", Title: "Health report", Desc: "Save a full diagnostic snapshot",
			Cmd: `mkdir -p "$HL_REPORT"; f="$HL_REPORT/health_$(date +%Y-%m-%d_%H-%M-%S).txt"; { echo 'homelab health report'; date; echo; uptime; echo; free -h; echo; df -h; echo; $D ps -a; echo; $D system df; echo; systemctl --failed --no-pager; echo; ip -brief address; echo; $S ss -tulpn; } | tee "$f"; echo; echo "Saved: $f"`},
		{ID: "dclean", Title: "Safe Docker cleanup", Desc: "Dangling images and build cache only", Confirm: "Remove dangling images and unused build cache. Containers and volumes are untouched.",
			Cmd: `$D system df; echo; $D image prune -f; $D builder prune -f; echo; $D system df`},
		{ID: "aclean", Title: "APT cleanup", Desc: "Autoremove and clear package cache", Confirm: "Remove unused packages and clear the package cache.",
			Cmd: `$S apt-get autoremove -y && $S apt-get autoclean`},
		{ID: "journal", Title: "Compact journal", Desc: "Keep the most recent 14 days", Confirm: "Vacuum the system journal down to 14 days.",
			Cmd: `$S journalctl --vacuum-time=14d`},
		{ID: "reboot", Title: "Reboot", Desc: "Restart the host", Danger: true, Confirm: "Reboot the machine now. homelab will exit.", Cmd: `$S systemctl reboot`},
		{ID: "poweroff", Title: "Power off", Desc: "Shut the host down", Danger: true, Confirm: "Power off the machine now. homelab will exit.", Cmd: `$S systemctl poweroff`},
	}},
	{Name: "Settings", Kind: kSettings},
}

// ───────────────────────── jobs ─────────────────────────

type Job struct {
	mu        sync.Mutex
	Title     string
	Lines     []string
	Running   bool
	Start     time.Time
	End       time.Time
	Code      int
	Step      int
	Steps     int
	StepLabel string
	Note      string
	Stop      bool
	killable  bool
	cmd       *exec.Cmd
	Scroll    int
}

var ansiRe = regexp.MustCompile(`\x1b\[[0-9;?]*[A-Za-z]`)

func (j *Job) add(s string) {
	s = ansiRe.ReplaceAllString(s, "")
	if i := strings.LastIndex(s, "\r"); i >= 0 {
		s = s[i+1:]
	}
	s = strings.ReplaceAll(s, "\t", "    ")
	j.mu.Lock()
	j.Lines = append(j.Lines, s)
	if len(j.Lines) > 5000 {
		j.Lines = j.Lines[len(j.Lines)-4000:]
	}
	j.mu.Unlock()
}

func (j *Job) set(f func()) { j.mu.Lock(); f(); j.mu.Unlock() }

func (a *App) env() []string {
	S, D := "sudo -n", "docker"
	if os.Geteuid() == 0 {
		S = ""
	}
	if a.sudoDocker {
		D = strings.TrimSpace(S + " docker")
	}
	return append(os.Environ(),
		"HL_ARR="+a.cfg.ArrDir, "HL_MEDIA="+a.cfg.MediaDir, "HL_BACKUP="+a.cfg.BackupDir,
		"HL_REPORT="+a.cfg.ReportDir, "HL_COMPOSE="+a.cfg.ComposeFile, "HL_VERSION="+version,
		"S="+S, "D="+D, "DEBIAN_FRONTEND=noninteractive", "COMPOSE_PARALLEL_LIMIT=1",
		"COMPOSE_PROGRESS=plain", "TERM=dumb", "NO_COLOR=1",
	)
}

const prelude = `NI="nice -n 19"; command -v ionice >/dev/null 2>&1 && NI="$NI ionice -c3"
IP=$(hostname -I 2>/dev/null | awk '{print $1}')
dc() { $D compose -f "$HL_COMPOSE" "$@"; }
dcn() { $NI $D compose -f "$HL_COMPOSE" "$@"; }
`

func (a *App) runScript(j *Job, script string, killable bool) int {
	cmd := exec.Command("sh", "-c", prelude+script)
	cmd.Env = a.env()
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	r, w, err := os.Pipe()
	if err != nil {
		j.add("cannot create pipe: " + err.Error())
		return 1
	}
	cmd.Stdout, cmd.Stderr = w, w
	if err := cmd.Start(); err != nil {
		w.Close()
		r.Close()
		j.add("cannot start: " + err.Error())
		return 1
	}
	w.Close()
	j.set(func() { j.cmd, j.killable = cmd, killable })
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 64*1024), 1<<20)
	for sc.Scan() {
		j.add(sc.Text())
		a.wake()
	}
	r.Close()
	err = cmd.Wait()
	j.set(func() { j.cmd = nil })
	if err != nil {
		if ee, ok := err.(*exec.ExitError); ok {
			return ee.ExitCode()
		}
		return 1
	}
	return 0
}

func (a *App) capture(script string) (string, error) {
	cmd := exec.Command("sh", "-c", prelude+script)
	cmd.Env = a.env()
	out, err := cmd.Output()
	return strings.TrimSpace(string(out)), err
}

func (j *Job) stopNow() {
	j.mu.Lock()
	j.Stop = true
	cmd, k := j.cmd, j.killable
	j.mu.Unlock()
	if cmd != nil && k && cmd.Process != nil {
		pid := cmd.Process.Pid
		_ = syscall.Kill(-pid, syscall.SIGTERM)
		go func() {
			time.Sleep(4 * time.Second)
			_ = syscall.Kill(-pid, syscall.SIGKILL)
		}()
	}
}

func (j *Job) stopped() bool { j.mu.Lock(); defer j.mu.Unlock(); return j.Stop }

// start a job in the background; only one runs at a time.
func (a *App) startJob(key, title string, fn func(j *Job) int) bool {
	a.mu.Lock()
	if a.running != nil {
		a.mu.Unlock()
		a.toast("Another task is still running — let it finish or press x to stop it", true)
		return false
	}
	j := &Job{Title: title, Running: true, Start: time.Now()}
	a.jobs[key] = j
	a.running = j
	a.mu.Unlock()
	go func() {
		code := fn(j)
		j.set(func() {
			j.Running, j.End, j.Code, j.Note = false, time.Now(), code, ""
			if j.Stop && code == 0 {
				j.Code = 130
			}
		})
		a.mu.Lock()
		a.running = nil
		a.mu.Unlock()
		a.forceRefreshNow()
		a.wake()
	}()
	return true
}

func (a *App) runOp(tab string, op Op) {
	key := tab + "/" + op.ID
	switch op.Special {
	case "update-stack":
		a.startJob(key, op.Title, func(j *Job) int { return a.updateStack(j, false) })
	case "update-os":
		a.startJob(key, op.Title, func(j *Job) int { return a.updateOS(j, 1, 1) })
	case "update-all":
		a.startJob(key, op.Title, func(j *Job) int { return a.updateStack(j, true) })
	default:
		a.startJob(key, op.Title, func(j *Job) int { return a.runScript(j, op.Cmd, true) })
	}
}

// ───────────────────────── gentle updater ─────────────────────────

func (a *App) updateOS(j *Job, step, steps int) int {
	j.set(func() { j.Step, j.Steps, j.StepLabel = step, steps, "Ubuntu packages" })
	j.add("▸ Ubuntu packages (lowest priority, cannot be interrupted once packages are unpacking)")
	code := a.runScript(j, `$S $NI apt-get update && $S $NI apt-get full-upgrade -y`, false)
	if code != 0 {
		j.add("✗ APT finished with errors — stopping before touching any container.")
	} else {
		j.add("✓ Ubuntu is up to date")
	}
	return code
}

// calm waits until load and memory are comfortable, then rests a little.
func (a *App) calm(j *Job, pause int) {
	deadline := time.Now().Add(120 * time.Second)
	for time.Now().Before(deadline) && !j.stopped() {
		l := readLoad()[0]
		_, total, _, avail := readMem()
		memOK := total == 0 || avail/total > 0.10
		if l <= float64(a.tel.Cores)*0.9 && memOK {
			break
		}
		j.set(func() { j.Note = fmt.Sprintf("letting the machine breathe · load %.1f", l) })
		time.Sleep(2 * time.Second)
	}
	for i := pause; i > 0 && !j.stopped(); i-- {
		j.set(func() { j.Note = fmt.Sprintf("resting %ds before the next service", i) })
		time.Sleep(time.Second)
	}
	j.set(func() { j.Note = "" })
}

// composeStates uses the configured Compose project, including stopped replicas.
// A mixed-state service is skipped rather than modifying a stopped replica.
func (a *App) composeStates() (map[string][]string, error) {
	out, err := a.capture(`dc ps --all --format '{{.Name}} {{.Service}} {{.State}}'`)
	if err != nil {
		return nil, err
	}
	states := map[string][]string{}
	for _, line := range strings.Split(out, "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		p := strings.Fields(line)
		if len(p) != 3 {
			return nil, fmt.Errorf("unexpected Compose status output")
		}
		states[p[1]] = append(states[p[1]], p[2])
	}
	return states, nil
}

func onlyRunning(states []string) bool {
	if len(states) != 1 {
		return false
	}
	for _, state := range states {
		if state != "running" {
			return false
		}
	}
	return true
}

// Do not allow Compose to restore missing configured replicas.
func (a *App) singleReplicaServices() (map[string]bool, error) {
	out, err := a.capture(`dc config --format json`)
	if err != nil {
		return nil, err
	}
	var cfg struct {
		Services map[string]struct {
			Scale  *int `json:"scale"`
			Deploy struct {
				Replicas *int `json:"replicas"`
			} `json:"deploy"`
		} `json:"services"`
	}
	if err := json.Unmarshal([]byte(out), &cfg); err != nil || cfg.Services == nil {
		return nil, fmt.Errorf("invalid Compose configuration")
	}
	result := map[string]bool{}
	for name, svc := range cfg.Services {
		result[name] = (svc.Scale == nil || *svc.Scale == 1) && (svc.Deploy.Replicas == nil || *svc.Deploy.Replicas == 1)
	}
	return result, nil
}

func (a *App) applyRunningService(j *Job, svc string) int {
	states, err := a.composeStates()
	if err != nil {
		j.add("✗ Could not recheck service state; nothing was recreated.")
		return 1
	}
	single, err := a.singleReplicaServices()
	if err != nil {
		j.add("✗ Could not recheck Compose scale; nothing was recreated.")
		return 1
	}
	if !onlyRunning(states[svc]) || !single[svc] {
		j.add("⊘ Skipped: service is stopped, missing, or scaled.")
		return 0
	}
	if j.stopped() {
		j.add("■ Stopped before recreation.")
		return 130
	}
	j.add("  recreating the running service if its image changed…")
	return a.runScript(j, `dcn up -d --no-deps --pull never "`+svc+`" 2>&1`, false)
}

func (a *App) updateStack(j *Job, withOS bool) int {
	if !exists(a.cfg.ComposeFile) {
		j.add("✗ Compose file not found: " + a.cfg.ComposeFile)
		j.add("  Set compose_file in " + configPath())
		return 1
	}
	svcOut, err := a.capture(`dc config --services`)
	if err != nil || svcOut == "" {
		j.add("✗ Could not read the services from the Compose file.")
		j.add("  Is Docker running, and is the file valid?")
		return 1
	}
	states, err := a.composeStates()
	if err != nil {
		j.add("✗ Could not inspect Compose service states; no images were pulled.")
		return 1
	}
	single, err := a.singleReplicaServices()
	if err != nil {
		j.add("✗ Could not inspect Compose scale; no images were pulled.")
		return 1
	}
	var svcs []string
	for _, s := range strings.Fields(svcOut) {
		if onlyRunning(states[s]) && single[s] {
			svcs = append(svcs, s)
		} else {
			j.add("⊘ Skipping " + s + ": stopped, missing, or scaled.")
		}
	}
	steps := len(svcs) + 1
	base := 0
	if withOS {
		steps++
		base = 1
		if c := a.updateOS(j, 1, steps); c != 0 || j.stopped() {
			return c
		}
		j.add("")
	}
	if singleLayerPulls() {
		j.add("Docker is limited to one layer download at a time.")
	} else {
		j.add("Docker may download several layers at once — enable single-layer pulls in Settings.")
	}
	j.add(fmt.Sprintf("Updating %d running services, one after another.", len(svcs)))
	fails := 0
	for i, s := range svcs {
		if j.stopped() {
			j.add("■ Stopped. Services already updated stay updated.")
			return 130
		}
		j.set(func() { j.Step, j.Steps, j.StepLabel = base+i+1, steps, s })
		j.add("")
		j.add(fmt.Sprintf("▸ %s  (%d/%d)", s, i+1, len(svcs)))
		current, err := a.composeStates()
		if err != nil {
			j.add("✗ Could not recheck service state; stopping safely.")
			return 1
		}
		if !onlyRunning(current[s]) {
			j.add("  ⊘ skipped: service is no longer fully running")
			continue
		}
		j.add("  pulling image…")
		if c := a.runScript(j, `dcn pull "`+s+`" 2>&1`, true); c != 0 {
			if j.stopped() {
				j.add("■ Stopped during pull. Nothing was recreated.")
				return 130
			}
			j.add("  ✗ pull failed — container left untouched")
			fails++
			continue
		}
		code := a.applyRunningService(j, s)
		if code == 130 {
			return code
		}
		if code != 0 {
			j.add("  ✗ could not update " + s)
			fails++
		} else {
			j.add("  ✓ update step finished")
		}
		if i < len(svcs)-1 {
			a.calm(j, a.cfg.PullPause)
		}
	}
	if fails > 0 {
		j.add(fmt.Sprintf("Finished with %d problem(s). Check the lines above.", fails))
		return 1
	}
	j.add("✓ Running-service update complete. Stopped and scaled services were skipped.")
	return 0
}

// ───────────────────────── containers ─────────────────────────

func (a *App) containerAction(c Container, action string) {
	key := "container/" + c.Name
	name := c.Name
	svc := c.Service
	switch action {
	case "Update this service":
		if svc == "" {
			a.toast(name+" is not managed by Compose", true)
			return
		}
		a.startJob(key, "Update "+name, func(j *Job) int {
			states, err := a.composeStates()
			single, scaleErr := a.singleReplicaServices()
			if err != nil || scaleErr != nil || !onlyRunning(states[svc]) || !single[svc] {
				j.add("✗ Service is not fully running in the configured Compose project; nothing was pulled.")
				return 1
			}
			// Do not use a name from another Compose project with the same service label.
			out, err := a.capture(`dc ps --all --format '{{.Name}} {{.Service}} {{.State}}'`)
			found := false
			for _, line := range strings.Split(out, "\n") {
				p := strings.Fields(line)
				if len(p) == 3 && p[0] == name && p[1] == svc && p[2] == "running" {
					found = true
				}
			}
			if err != nil || !found {
				j.add("✗ Container is not running in the configured Compose project; nothing was pulled.")
				return 1
			}
			j.set(func() { j.Step, j.Steps, j.StepLabel = 1, 1, "pulling" })
			j.add("▸ " + svc + ": pulling image…")
			if cd := a.runScript(j, `dcn pull "`+svc+`" 2>&1`, true); cd != 0 {
				j.add("✗ pull failed — container untouched")
				return cd
			}
			return a.applyRunningService(j, svc)
		})
	case "Start":
		a.startJob(key, "Start "+name, func(j *Job) int { return a.runScript(j, `$D start "`+name+`" 2>&1`, true) })
	case "Stop":
		a.startJob(key, "Stop "+name, func(j *Job) int { return a.runScript(j, `$D stop "`+name+`" 2>&1`, false) })
	case "Restart":
		a.startJob(key, "Restart "+name, func(j *Job) int { return a.runScript(j, `$D restart "`+name+`" 2>&1`, false) })
	case "Inspect":
		a.startJob(key, "Inspect "+name, func(j *Job) int { return a.runScript(j, `$D inspect "`+name+`" 2>&1`, true) })
	case "Logs":
		a.startJob(key, "Logs "+name, func(j *Job) int { return a.runScript(j, `$D logs --tail 250 "`+name+`" 2>&1`, true) })
	}
}

// ───────────────────────── single-layer pulls ─────────────────────────

func (a *App) enableSingleLayer(j *Job) int {
	j.add("▸ Reading /etc/docker/daemon.json")
	raw, readErr := a.capture(`if [ -e /etc/docker/daemon.json ]; then cat /etc/docker/daemon.json; else echo '{}'; fi`)
	if readErr != nil {
		j.add("✗ Cannot read Docker configuration; no changes made.")
		return 1
	}
	m := map[string]any{}
	if strings.TrimSpace(raw) != "" {
		if err := json.Unmarshal([]byte(raw), &m); err != nil {
			j.add("✗ daemon.json is not valid JSON, refusing to touch it: " + err.Error())
			return 1
		}
	}
	if m == nil {
		j.add("✗ Docker configuration must be a JSON object; no changes made.")
		return 1
	}
	m["max-concurrent-downloads"] = 1
	m["max-concurrent-uploads"] = 1
	out, _ := json.MarshalIndent(m, "", "  ")
	f, err := os.CreateTemp("", "homelab-daemon-*.json")
	if err != nil {
		j.add("✗ " + err.Error())
		return 1
	}
	tmp := f.Name()
	defer os.Remove(tmp)
	if _, err := f.Write(append(out, '\n')); err != nil {
		f.Close()
		j.add("✗ " + err.Error())
		return 1
	}
	if err := f.Close(); err != nil {
		j.add("✗ " + err.Error())
		return 1
	}
	quoted := "'" + strings.ReplaceAll(tmp, "'", "'\\''") + "'"
	j.add("▸ Backing up the old file and installing the new one")
	if c := a.runScript(j, `if [ -f /etc/docker/daemon.json ]; then $S cp -n /etc/docker/daemon.json /etc/docker/daemon.json.homelab.bak || exit 1; fi; $S install -m 644 `+quoted+` /etc/docker/daemon.json && echo installed`, false); c != 0 {
		return c
	}
	j.add("▸ Reloading Docker")
	c := a.runScript(j, `($S systemctl reload docker 2>&1 || { pid=$(pidof dockerd) && test -n "$pid" && $S kill -HUP "$pid" 2>&1; }) && echo reloaded`, false)
	if c == 0 {
		j.add("✓ Docker now downloads one layer at a time")
	}
	return c
}
