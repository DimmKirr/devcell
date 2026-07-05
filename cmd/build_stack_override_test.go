package main

import (
	"strings"
	"testing"
)

// CELL-93: --stack flag and DEVCELL_STACK env both override the TOML-resolved
// stack. Precedence: flag > env > "" (empty = use TOML/default at caller).

func TestResolveStackOverride_EmptyWhenUnset(t *testing.T) {
	got, err := resolveStackOverride("", func(string) string { return "" })
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if got != "" {
		t.Errorf("want empty (no override), got %q", got)
	}
}

func TestResolveStackOverride_FlagWinsOverEnv(t *testing.T) {
	got, err := resolveStackOverride("go", func(k string) string {
		if k == "DEVCELL_STACK" {
			return "python"
		}
		return ""
	})
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if got != "go" {
		t.Errorf("flag must beat env; want go, got %q", got)
	}
}

func TestResolveStackOverride_EnvWhenFlagEmpty(t *testing.T) {
	got, err := resolveStackOverride("", func(k string) string {
		if k == "DEVCELL_STACK" {
			return "python"
		}
		return ""
	})
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if got != "python" {
		t.Errorf("env should win when flag is empty; want python, got %q", got)
	}
}

func TestResolveStackOverride_UnknownFlagRejected(t *testing.T) {
	_, err := resolveStackOverride("bogus", func(string) string { return "" })
	if err == nil {
		t.Fatal("expected error for unknown stack")
	}
	msg := err.Error()
	if !strings.Contains(msg, "bogus") {
		t.Errorf("error must mention the bad stack name; got %q", msg)
	}
	if !strings.Contains(msg, "available") {
		t.Errorf("error must list available stacks; got %q", msg)
	}
}

func TestResolveStackOverride_UnknownEnvRejected(t *testing.T) {
	_, err := resolveStackOverride("", func(k string) string {
		if k == "DEVCELL_STACK" {
			return "bogus"
		}
		return ""
	})
	if err == nil {
		t.Fatal("expected error for unknown stack via env")
	}
	if !strings.Contains(err.Error(), "bogus") {
		t.Errorf("error must mention the bad stack name; got %q", err.Error())
	}
}

func TestResolveStackOverride_AllKnownStacksAccepted(t *testing.T) {
	for _, s := range []string{"base", "go", "node", "python", "fullstack", "electronics", "ultimate"} {
		got, err := resolveStackOverride(s, func(string) string { return "" })
		if err != nil {
			t.Errorf("known stack %q rejected: %v", s, err)
		}
		if got != s {
			t.Errorf("want %q, got %q", s, got)
		}
	}
}
