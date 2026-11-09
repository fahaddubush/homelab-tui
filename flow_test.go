package main

import (
	"os"
	"strings"
	"testing"
	"time"
)

func newTestApp(t *testing.T) *App {
	a := &App{cfg: loadConfig(), jobs: map[string]*Job{}, disp: map[string]float64{},
		forceRefresh: make(chan struct{}, 1), wakeCh: make(chan struct{}, 8), quit: make(chan struct{}, 4),
		start: time.Now(), now: time.Now()}
	a.tel.Cores = 64 // never "busy" in tests
	return a
}

func TestSequentialUpdate(t *testing.T) {
	dir := t.TempDir()
	log := dir + "/calls.log"
	os.WriteFile(dir+"/docker", []byte(`#!/bin/sh
echo "$(date +%s.%N) docker $*" >> `+log+`
case "$*" in
 *"config --services"*) printf 'alpha\nbeta\ngamma\nstopped\nmixed\nmissing\n';;
 *"config --format json"*) echo '{"services":{"alpha":{},"beta":{},"gamma":{},"stopped":{},"mixed":{},"missing":{}}}';;
 *"ps --all"*) printf 'alpha-1 alpha running\nbeta-1 beta running\ngamma-1 gamma running\nstopped-1 stopped exited\nmixed-1 mixed running\nmixed-2 mixed exited\n';;
 *pull*beta*) echo "boom"; exit 1;;
 *) echo "ok $*";;
esac
`), 0o755)
	os.WriteFile(dir+"/compose.yml", []byte("services: {}"), 0o644)
	t.Setenv("PATH", dir+":"+os.Getenv("PATH"))
	a := newTestApp(t)
	a.cfg.ComposeFile = dir + "/compose.yml"
	a.cfg.PullPause = 0
	j := &Job{Running: true}
	code := a.updateStack(j, false)
	b, _ := os.ReadFile(log)
	calls := strings.Split(strings.TrimSpace(string(b)), "\n")
	var seq []string
	for _, c := range calls {
		f := strings.Fields(c)
		seq = append(seq, strings.Join(f[2:], " "))
	}
	got := strings.Join(seq, "\n")
	t.Log("\n" + got)
	t.Log("\n" + strings.Join(j.Lines, "\n"))
	// each service: pull then up, strictly in order; beta failed pull → no up for beta
	want := []string{"pull alpha", "up -d --no-deps --pull never alpha", "pull beta", "pull gamma", "up -d --no-deps --pull never gamma"}
	idx := 0
	for _, s := range seq {
		if idx < len(want) && strings.Contains(s, want[idx]) {
			idx++
		}
	}
	if idx != len(want) {
		t.Fatalf("order wrong, matched %d of %d", idx, len(want))
	}
	if strings.Contains(got, "image prune") || strings.Contains(got, "up -d --no-deps --pull never beta") {
		t.Fatal("failed pull must not recreate, and images must not be pruned")
	}
	for _, s := range []string{"stopped", "mixed", "missing"} {
		if strings.Contains(got, "pull "+s) {
			t.Fatalf("pulled skipped service %s", s)
		}
	}
	if code != 1 {
		t.Fatalf("expected exit 1 for a failed pull, got %d", code)
	}
}

func TestStopBetweenServices(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(dir+"/docker", []byte("#!/bin/sh\ncase \"$*\" in *'config --services'*) printf 'a\\nb\\n';; *'config --format json'*) echo '{\"services\":{\"a\":{},\"b\":{}}}';; *ps*) printf 'a-1 a running\\nb-1 b running\\n';; *) sleep 1;; esac\n"), 0o755)
	os.WriteFile(dir+"/compose.yml", []byte("x"), 0o644)
	t.Setenv("PATH", dir+":"+os.Getenv("PATH"))
	a := newTestApp(t)
	a.cfg.ComposeFile = dir + "/compose.yml"
	j := &Job{Running: true}
	go func() { time.Sleep(300 * time.Millisecond); j.stopNow() }()
	code := a.updateStack(j, false)
	if code != 130 {
		t.Fatalf("expected 130 got %d\n%s", code, strings.Join(j.Lines, "\n"))
	}
}

func TestStackFailsClosedWhenStatusUnavailable(t *testing.T) {
	dir := t.TempDir()
	log := dir + "/calls"
	os.WriteFile(dir+"/docker", []byte("#!/bin/sh\necho \"$*\" >> "+log+"\ncase \"$*\" in *config*) echo alpha;; *ps*) exit 1;; esac\n"), 0o755)
	os.WriteFile(dir+"/compose.yml", []byte("services: {}"), 0o644)
	t.Setenv("PATH", dir+":"+os.Getenv("PATH"))
	a := newTestApp(t)
	a.cfg.ComposeFile = dir + "/compose.yml"
	if code := a.updateStack(&Job{}, false); code == 0 {
		t.Fatal("status failure must fail the update")
	}
	b, _ := os.ReadFile(log)
	if strings.Contains(string(b), "pull") || strings.Contains(string(b), " up ") {
		t.Fatal("status failure must not pull or recreate")
	}
}

func TestOnlyRunningFailsClosed(t *testing.T) {
	for _, tc := range []struct {
		states []string
		want   bool
	}{
		{nil, false}, {[]string{"exited"}, false}, {[]string{"running", "exited"}, false},
		{[]string{"running"}, true}, {[]string{"running", "running"}, false}, {[]string{"unknown"}, false},
	} {
		if got := onlyRunning(tc.states); got != tc.want {
			t.Fatalf("onlyRunning(%v)=%v", tc.states, got)
		}
	}
}

func TestSkipIfStoppedDuringPull(t *testing.T) {
	dir := t.TempDir()
	log := dir + "/calls"
	state := dir + "/stopped"
	script := `#!/bin/sh
echo "$*" >> ` + log + `
case "$*" in
 *"config --services"*) echo alpha;;
 *"config --format json"*) echo '{"services":{"alpha":{}}}';;
 *"ps --all"*) if [ -f ` + state + ` ]; then echo 'alpha-1 alpha exited'; else echo 'alpha-1 alpha running'; fi;;
 *"pull alpha"*) touch ` + state + `;;
esac
`
	os.WriteFile(dir+"/docker", []byte(script), 0o755)
	os.WriteFile(dir+"/compose.yml", []byte("services: {}"), 0o644)
	t.Setenv("PATH", dir+":"+os.Getenv("PATH"))
	a := newTestApp(t)
	a.cfg.ComposeFile = dir + "/compose.yml"
	a.cfg.PullPause = 0
	j := &Job{}
	if code := a.updateStack(j, false); code != 0 {
		t.Fatalf("unexpected failure %d", code)
	}
	b, _ := os.ReadFile(log)
	if strings.Contains(string(b), " up ") {
		t.Fatal("recreated a service stopped during pull")
	}
	if !strings.Contains(strings.Join(j.Lines, "\n"), "Skipped") {
		t.Fatal("skip was not reported")
	}
}

func TestConfiguredScaleIsChecked(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(dir+"/docker", []byte("#!/bin/sh\necho '{\"services\":{\"one\":{},\"scaled\":{\"scale\":2},\"deployscaled\":{\"deploy\":{\"replicas\":2}}}}'\n"), 0o755)
	t.Setenv("PATH", dir+":"+os.Getenv("PATH"))
	a := newTestApp(t)
	single, err := a.singleReplicaServices()
	if err != nil || !single["one"] || single["scaled"] || single["deployscaled"] {
		t.Fatalf("bad scale result: %v %v", single, err)
	}
}

func TestCombinedUpdateOrdersOSBeforeContainers(t *testing.T) {
	dir := t.TempDir()
	log := dir + "/calls"
	files := map[string]string{
		"sudo":    "#!/bin/sh\n[ \"$1\" = -n ] && shift\nexec \"$@\"\n",
		"apt-get": "#!/bin/sh\necho apt-$* >> " + log + "\n",
		"docker":  "#!/bin/sh\necho docker-$* >> " + log + "\ncase \"$*\" in *'config --services'*) echo alpha;; *'config --format json'*) echo '{\"services\":{\"alpha\":{}}}';; *'ps --all'*) echo 'alpha-1 alpha running';; esac\n",
	}
	for name, script := range files {
		os.WriteFile(dir+"/"+name, []byte(script), 0o755)
	}
	os.WriteFile(dir+"/compose.yml", []byte("services: {}"), 0o644)
	t.Setenv("PATH", dir+":"+os.Getenv("PATH"))
	a := newTestApp(t)
	a.cfg.ComposeFile = dir + "/compose.yml"
	a.cfg.PullPause = 0
	j := &Job{}
	if code := a.updateStack(j, true); code != 0 {
		t.Fatalf("combined update failed: %d %v", code, j.Lines)
	}
	b, _ := os.ReadFile(log)
	calls := string(b)
	apt := strings.Index(calls, "apt-full-upgrade")
	pull := strings.Index(calls, "pull alpha")
	up := strings.Index(calls, "up -d --no-deps --pull never alpha")
	if apt < 0 || pull < apt || up < pull {
		t.Fatalf("wrong order: %s", calls)
	}
}

func TestOpsScriptsParse(t *testing.T) {
	for _, tb := range tabs {
		for _, op := range tb.Ops {
			if op.Cmd == "" {
				continue
			}
			a := newTestApp(t)
			cmd := "sh -n"
			_ = cmd
			f := t.TempDir() + "/s.sh"
			os.WriteFile(f, []byte(prelude+op.Cmd), 0o644)
			out, err := a.capture("sh -n " + f)
			if err != nil {
				t.Errorf("%s/%s: syntax error: %s", tb.Name, op.ID, out)
			}
		}
	}
}

func TestKeys(t *testing.T) {
	ks := parseKeys([]byte("\x1b[A\x1b[<65;10;5Mq\x1b"))
	names := []string{}
	for _, k := range ks {
		names = append(names, k.name)
	}
	if strings.Join(names, ",") != "up,wheeldown,q,esc" {
		t.Fatal(names)
	}
}

func TestRealOps(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("HOME", dir)
	a := newTestApp(t)
	a.cfg.MediaDir, a.cfg.BackupDir, a.cfg.ArrDir = dir, dir+"/bk", dir+"/arr"
	os.MkdirAll(a.cfg.ArrDir, 0o755)
	os.WriteFile(a.cfg.ArrDir+"/x.yml", []byte("a"), 0o644)
	for _, id := range []string{"Storage/media", "Backups/essential", "Backups/list", "Backups/verify"} {
		var op Op
		for _, tb := range tabs {
			for _, o := range tb.Ops {
				if tb.Name+"/"+o.ID == id {
					op = o
				}
			}
		}
		j := &Job{}
		code := a.runScript(j, op.Cmd, true)
		t.Logf("%s → %d\n  %s", id, code, strings.Join(j.Lines, "\n  "))
		if code != 0 {
			t.Errorf("%s failed", id)
		}
	}
}
