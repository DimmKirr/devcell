package session_test

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/DimmKirr/devcell/internal/session"
)

func TestBegin_WritesJSON(t *testing.T) {
	dir := t.TempDir()
	before := time.Now().UTC().Add(-time.Second)

	rec, err := session.Begin(dir, "claude", []string{"-c"})
	if err != nil {
		t.Fatalf("Begin: %v", err)
	}

	data, err := os.ReadFile(filepath.Join(dir, ".devcell", "cell.json"))
	if err != nil {
		t.Fatalf("read cell.json: %v", err)
	}

	var got session.Record
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	if got.Tool != "claude" {
		t.Errorf("tool: want %q, got %q", "claude", got.Tool)
	}
	if len(got.Args) != 1 || got.Args[0] != "-c" {
		t.Errorf("args: want [\"-c\"], got %v", got.Args)
	}
	if got.Started.Before(before) {
		t.Errorf("started %v before test start %v", got.Started, before)
	}
	if got.Stopped != nil {
		t.Errorf("stopped: want nil, got %v", got.Stopped)
	}
	if got.Clean != nil {
		t.Errorf("clean: want nil, got %v", got.Clean)
	}
	if rec.Tool != "claude" {
		t.Errorf("returned record tool: want %q, got %q", "claude", rec.Tool)
	}
}

func TestBegin_NilArgs_WritesEmptyArray(t *testing.T) {
	dir := t.TempDir()

	_, err := session.Begin(dir, "claude", nil)
	if err != nil {
		t.Fatalf("Begin: %v", err)
	}

	data, err := os.ReadFile(filepath.Join(dir, ".devcell", "cell.json"))
	if err != nil {
		t.Fatalf("read cell.json: %v", err)
	}

	// Must serialize as [] not null
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if string(raw["args"]) != "[]" {
		t.Errorf("args: want [], got %s", raw["args"])
	}
}

func TestBegin_CreatesDevcellDir(t *testing.T) {
	dir := t.TempDir()
	devcellDir := filepath.Join(dir, ".devcell")

	// Confirm dir doesn't exist yet
	if _, err := os.Stat(devcellDir); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf(".devcell should not exist yet")
	}

	_, err := session.Begin(dir, "shell", []string{})
	if err != nil {
		t.Fatalf("Begin: %v", err)
	}

	info, err := os.Stat(devcellDir)
	if err != nil {
		t.Fatalf(".devcell not created: %v", err)
	}
	if !info.IsDir() {
		t.Fatalf(".devcell is not a directory")
	}
}

// --- Finish tests ---

func TestFinish_CleanExit(t *testing.T) {
	dir := t.TempDir()
	rec, err := session.Begin(dir, "claude", []string{"-c"})
	if err != nil {
		t.Fatalf("Begin: %v", err)
	}

	if err := rec.Finish(dir, nil); err != nil {
		t.Fatalf("Finish: %v", err)
	}

	data, err := os.ReadFile(filepath.Join(dir, ".devcell", "cell.json"))
	if err != nil {
		t.Fatalf("read: %v", err)
	}

	var got session.Record
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	if got.Stopped == nil {
		t.Fatal("stopped: want non-nil, got nil")
	}
	if got.Clean == nil || !*got.Clean {
		t.Errorf("clean: want true, got %v", got.Clean)
	}
	if got.Tool != "claude" {
		t.Errorf("tool preserved: want %q, got %q", "claude", got.Tool)
	}
	if len(got.Args) != 1 || got.Args[0] != "-c" {
		t.Errorf("args preserved: want [\"-c\"], got %v", got.Args)
	}
}

func TestFinish_ErrorExit(t *testing.T) {
	dir := t.TempDir()
	rec, err := session.Begin(dir, "gemini", []string{"--resume", "foo"})
	if err != nil {
		t.Fatalf("Begin: %v", err)
	}

	if err := rec.Finish(dir, errors.New("exit status 1")); err != nil {
		t.Fatalf("Finish: %v", err)
	}

	data, err := os.ReadFile(filepath.Join(dir, ".devcell", "cell.json"))
	if err != nil {
		t.Fatalf("read: %v", err)
	}

	var got session.Record
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	if got.Clean == nil || *got.Clean {
		t.Errorf("clean: want false, got %v", got.Clean)
	}
	if got.Tool != "gemini" {
		t.Errorf("tool preserved: want %q, got %q", "gemini", got.Tool)
	}
	if len(got.Args) != 2 || got.Args[0] != "--resume" || got.Args[1] != "foo" {
		t.Errorf("args preserved: want [\"--resume\", \"foo\"], got %v", got.Args)
	}
}

// --- Read tests ---

func TestRead_Success(t *testing.T) {
	dir := t.TempDir()
	_, err := session.Begin(dir, "codex", []string{"--model", "o3"})
	if err != nil {
		t.Fatalf("Begin: %v", err)
	}

	rec, err := session.Read(dir)
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if rec.Tool != "codex" {
		t.Errorf("tool: want %q, got %q", "codex", rec.Tool)
	}
	if len(rec.Args) != 2 || rec.Args[0] != "--model" {
		t.Errorf("args: want [\"--model\", \"o3\"], got %v", rec.Args)
	}
}

func TestRead_NotExist(t *testing.T) {
	dir := t.TempDir()
	_, err := session.Read(dir)
	if !errors.Is(err, os.ErrNotExist) {
		t.Errorf("want os.ErrNotExist, got %v", err)
	}
}

func TestRead_MalformedJSON(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, ".devcell")
	os.MkdirAll(p, 0755)
	os.WriteFile(filepath.Join(p, "cell.json"), []byte("{bad json"), 0644)

	_, err := session.Read(dir)
	if err == nil {
		t.Fatal("want error for malformed JSON, got nil")
	}
}
