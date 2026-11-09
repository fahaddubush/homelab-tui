package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func findTestOp(t *testing.T, tab, id string) Op {
	t.Helper()
	for _, tb := range tabs {
		if tb.Name == tab {
			for _, op := range tb.Ops {
				if op.ID == id {
					return op
				}
			}
		}
	}
	t.Fatalf("missing %s/%s", tab, id)
	return Op{}
}

func TestDemoCannotActivateOperations(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)
	marker := filepath.Join(dir, "docker-called")
	os.WriteFile(filepath.Join(dir, "docker"), []byte("#!/bin/sh\ntouch "+marker+"\n"), 0755)
	t.Setenv("PATH", dir+":"+os.Getenv("PATH"))
	a := newTestApp(t)
	a.demo = true
	a.tab = 2
	a.activate()
	if a.running != nil || len(a.jobs) != 0 || a.modal != nil {
		t.Fatal("demo created an operation or confirmation")
	}
	a.tab = 1
	a.tel.Containers = []Container{{Name: "sample", State: "running"}}
	a.activate()
	a.tab = 9
	a.activate()
	if _, err := os.Stat(configPath()); !os.IsNotExist(err) {
		t.Fatalf("demo wrote config or unexpected error: %v", err)
	}
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatalf("demo invoked Docker or unexpected error: %v", err)
	}
}

func TestPortsShowDockerBindings(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "docker"), []byte("#!/bin/sh\necho 'example-service 127.0.0.1:4321->80/tcp'\n"), 0755)
	t.Setenv("PATH", dir+":"+os.Getenv("PATH"))
	a := newTestApp(t)
	j := &Job{}
	if code := a.runScript(j, findTestOp(t, "Network", "addr").Cmd, true); code != 0 {
		t.Fatalf("failed: %v", j.Lines)
	}
	output := strings.Join(j.Lines, "\n")
	if !strings.Contains(output, "example-service 127.0.0.1:4321->80/tcp") || strings.Contains(output, "http://") {
		t.Fatalf("unexpected port output: %s", output)
	}
}

func TestBackupExcludesCustomNestedBackupDirectory(t *testing.T) {
	dir := t.TempDir()
	source := filepath.Join(dir, "project")
	backup := filepath.Join(source, "custom", "archive")
	if err := os.MkdirAll(backup, 0755); err != nil {
		t.Fatal(err)
	}
	os.WriteFile(filepath.Join(source, "settings.txt"), []byte("fixture"), 0600)
	os.WriteFile(filepath.Join(backup, "previous.tar.gz"), []byte("previous backup"), 0600)
	a := newTestApp(t)
	a.cfg.ArrDir = source
	a.cfg.BackupDir = backup
	j := &Job{}
	if code := a.runScript(j, findTestOp(t, "Backups", "full").Cmd, true); code != 0 {
		t.Fatalf("backup failed: %d %v", code, j.Lines)
	}
	files, _ := filepath.Glob(filepath.Join(backup, "homelab-full_*.tar.gz"))
	if len(files) != 1 {
		t.Fatalf("archive count: %v", files)
	}
	listed, err := exec.Command("tar", "-tzf", files[0]).CombinedOutput()
	if err != nil {
		t.Fatal(err, string(listed))
	}
	if !strings.Contains(string(listed), "settings.txt") || strings.Contains(string(listed), "previous.tar.gz") {
		t.Fatalf("unexpected archive contents: %s", listed)
	}
}

func TestBackupPruneOnlyOwnedOldArchives(t *testing.T) {
	dir := t.TempDir()
	nested := filepath.Join(dir, "nested")
	os.MkdirAll(nested, 0755)
	for _, name := range []string{"homelab-full_old.tar.gz", "other.tar.gz", "nested/homelab-full_old.tar.gz"} {
		p := filepath.Join(dir, name)
		os.WriteFile(p, []byte("fixture"), 0600)
		old := time.Now().Add(-40 * 24 * time.Hour)
		os.Chtimes(p, old, old)
	}
	a := newTestApp(t)
	a.cfg.BackupDir = dir
	j := &Job{}
	if code := a.runScript(j, findTestOp(t, "Backups", "prune").Cmd, true); code != 0 {
		t.Fatalf("prune failed: %v", j.Lines)
	}
	if _, err := os.Stat(filepath.Join(dir, "homelab-full_old.tar.gz")); !os.IsNotExist(err) {
		t.Fatal("old owned archive not deleted")
	}
	for _, name := range []string{"other.tar.gz", "nested/homelab-full_old.tar.gz"} {
		if _, err := os.Stat(filepath.Join(dir, name)); err != nil {
			t.Fatalf("unrelated file removed: %s", name)
		}
	}
}
