package main

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeConfig(t *testing.T, body string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "plugins.json")
	if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestConfig_ReportsEveryProblem(t *testing.T) {
	path := writeConfig(t, `{"plugins": [
		{"id": "a", "use": "claude-status", "keys": [0]},
		{"id": "b", "use": "claude-status", "keys": [0]},
		{"id": "c", "use": "nope", "keys": [1]},
		{"id": "a", "use": "pager", "keys": [2]},
		{"id": "d", "use": "pager", "keys": [7]}
	]}`)
	var stdout, stderr bytes.Buffer
	if code := run(context.Background(), []string{"--config", path}, &stdout, &stderr); code != 2 {
		t.Fatalf("exit code %d, want 2", code)
	}
	for _, want := range []string{
		`key 0 on page "main" is already owned by a`,
		`unknown plugin "nope"`,
		`a: duplicate id`,
		`key 7 is outside 0..5`,
	} {
		if !strings.Contains(stderr.String(), want) {
			t.Errorf("stderr misses %q:\n%s", want, stderr.String())
		}
	}
}

func TestCheck_AcceptsTheExampleConfig(t *testing.T) {
	var stdout, stderr bytes.Buffer
	if code := run(context.Background(), []string{"--config", "example.json", "--check"}, &stdout, &stderr); code != 0 {
		t.Fatalf("exit code %d, stderr:\n%s", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), "ok, 3 plugin instances on pages [work agents]") {
		t.Errorf("stdout = %q", stdout.String())
	}
}

func TestList_PrintsEveryPlugin(t *testing.T) {
	var stdout, stderr bytes.Buffer
	if code := run(context.Background(), []string{"--list"}, &stdout, &stderr); code != 0 {
		t.Fatalf("exit code %d", code)
	}
	for _, f := range registry {
		if !strings.Contains(stdout.String(), f.Name) {
			t.Errorf("--list misses %s:\n%s", f.Name, stdout.String())
		}
	}
}

func TestMissingConfig_Exits2(t *testing.T) {
	var stdout, stderr bytes.Buffer
	if code := run(context.Background(), []string{"--config", filepath.Join(t.TempDir(), "none.json")}, &stdout, &stderr); code != 2 {
		t.Fatalf("exit code %d, want 2", code)
	}
}
