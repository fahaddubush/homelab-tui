package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestComposeDiscoveryAndAmbiguity(t *testing.T) {
	home, cwd := t.TempDir(), t.TempDir()
	t.Setenv("HOMELAB_COMPOSE_FILE", "")
	file := filepath.Join(cwd, "compose.yaml")
	os.WriteFile(file, []byte("services: {}"), 0600)
	if got := discoverCompose(home, cwd); got != file {
		t.Fatalf("expected cwd file, got %q", got)
	}
	os.WriteFile(filepath.Join(cwd, "compose.yml"), []byte("services: {}"), 0600)
	if got := discoverCompose(home, cwd); got != "" {
		t.Fatalf("ambiguous files must fail closed: %q", got)
	}
	chosen := filepath.Join(home, "chosen.yaml")
	t.Setenv("HOMELAB_COMPOSE_FILE", chosen)
	if got := discoverCompose(home, cwd); got != chosen {
		t.Fatalf("explicit path must win: %q", got)
	}
}

func TestConfigOverridesAndMalformedConfigFailsClosed(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("HOME", dir)
	t.Setenv("XDG_CONFIG_HOME", dir)
	t.Setenv("HOMELAB_COMPOSE_FILE", filepath.Join(dir, "detected.yml"))
	path := configPath()
	os.MkdirAll(filepath.Dir(path), 0700)
	os.WriteFile(path, []byte(`{"compose_file":"/custom/compose.yml","media_dir":"/custom/media","animations":false}`), 0600)
	c := loadConfig()
	if c.ComposeFile != "/custom/compose.yml" || c.MediaDir != "/custom/media" || c.Animations {
		t.Fatalf("explicit config lost: %+v", c)
	}
	if c.ArrDir != "/custom" || c.BackupDir != "/custom/backups" || c.ReportDir != "/custom/reports" {
		t.Fatalf("paths not derived from explicit Compose file: %+v", c)
	}
	os.WriteFile(path, []byte(`{"compose_file":"/partial",`), 0600)
	if got := loadConfig().ComposeFile; got != "" {
		t.Fatalf("malformed config must disable Compose: %q", got)
	}
	os.WriteFile(path, []byte("null"), 0600)
	if got := loadConfig().ComposeFile; got != "" {
		t.Fatalf("null config must disable Compose: %q", got)
	}
	os.WriteFile(path, []byte(`{"compose_file":null}`), 0600)
	if got := loadConfig().ComposeFile; got != "" {
		t.Fatalf("null compose_file must disable Compose: %q", got)
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(path, 0700); err != nil {
		t.Fatal(err)
	}
	if got := loadConfig().ComposeFile; got != "" {
		t.Fatalf("unreadable config must disable Compose: %q", got)
	}
}

func TestDockerUnnamedFirstContainerKeepsColumns(t *testing.T) {
	dir := t.TempDir()
	script := "#!/bin/sh\nprintf '\\tsha256:123\\tdead\\tDead\\t\\t\\nnamed\\timage\\trunning\\tUp\\t80/tcp\\tweb\\n'\n"
	os.WriteFile(filepath.Join(dir, "docker"), []byte(script), 0700)
	t.Setenv("PATH", dir+":"+os.Getenv("PATH"))
	list, err := (&App{}).readContainers()
	if err != nil || len(list) != 2 {
		t.Fatalf("read containers: %v, %+v", err, list)
	}
	if list[0].Name != "" || list[0].Image != "sha256:123" || list[0].State != "dead" {
		t.Fatalf("columns shifted: %+v", list[0])
	}
	if list[1].Name != "named" || list[1].Service != "web" {
		t.Fatalf("named row broken: %+v", list[1])
	}
}

func TestDemoIdentityIsDocumentationOnly(t *testing.T) {
	a := &App{}
	demoTelemetry(a)
	if a.tel.IP != "192.0.2.10" || !strings.HasPrefix(a.tel.Hostname, "demo-") {
		t.Fatal("demo identity should be clearly fictional")
	}
}
