package cfg

import (
	"strings"
	"testing"
)

// fakeLookup implements the lookup contract: returns (value, ok).
// ok=false means unset; ok=true with empty string means set-but-empty
// (which ExpandEnv must treat as a miss).
func fakeLookup(m map[string]string) func(string) (string, bool) {
	return func(k string) (string, bool) {
		v, ok := m[k]
		return v, ok
	}
}

func TestExpandEnv_Plain_PassesThrough(t *testing.T) {
	env := map[string]string{"FOO": "literal"}
	if err := ExpandEnv(env, fakeLookup(nil)); err != nil {
		t.Fatalf("plain value should not error, got %v", err)
	}
	if env["FOO"] != "literal" {
		t.Errorf("plain value mutated: %q", env["FOO"])
	}
}

func TestExpandEnv_BracedVar_Resolves(t *testing.T) {
	env := map[string]string{"KC": "${KUBECONFIG}"}
	if err := ExpandEnv(env, fakeLookup(map[string]string{"KUBECONFIG": "/path"})); err != nil {
		t.Fatalf("err = %v", err)
	}
	if env["KC"] != "/path" {
		t.Errorf("KC = %q, want /path", env["KC"])
	}
}

func TestExpandEnv_ShortVar_Resolves(t *testing.T) {
	env := map[string]string{"KC": "$KUBECONFIG"}
	if err := ExpandEnv(env, fakeLookup(map[string]string{"KUBECONFIG": "/path"})); err != nil {
		t.Fatalf("err = %v", err)
	}
	if env["KC"] != "/path" {
		t.Errorf("KC = %q, want /path", env["KC"])
	}
}

func TestExpandEnv_Missing_ReturnsErrorWithPath(t *testing.T) {
	env := map[string]string{"KC": "${KUBECONFIG}"}
	err := ExpandEnv(env, fakeLookup(nil))
	if err == nil {
		t.Fatal("expected error for missing var")
	}
	paths, ok := err.Refs["KUBECONFIG"]
	if !ok {
		t.Fatalf("Refs missing KUBECONFIG key: %v", err.Refs)
	}
	if len(paths) != 1 || paths[0] != "[env].KC" {
		t.Errorf("path = %v, want [[env].KC]", paths)
	}
}

func TestExpandEnv_SetButEmpty_IsMiss(t *testing.T) {
	env := map[string]string{"KC": "${KUBECONFIG}"}
	err := ExpandEnv(env, fakeLookup(map[string]string{"KUBECONFIG": ""}))
	if err == nil {
		t.Fatal("set-but-empty should be treated as miss")
	}
	if _, ok := err.Refs["KUBECONFIG"]; !ok {
		t.Errorf("Refs should include KUBECONFIG: %v", err.Refs)
	}
}

func TestExpandEnv_MultipleMisses_AllReported(t *testing.T) {
	env := map[string]string{"A": "${X}", "B": "${Y}"}
	err := ExpandEnv(env, fakeLookup(nil))
	if err == nil {
		t.Fatal("expected error")
	}
	if _, ok := err.Refs["X"]; !ok {
		t.Errorf("Refs should include X: %v", err.Refs)
	}
	if _, ok := err.Refs["Y"]; !ok {
		t.Errorf("Refs should include Y: %v", err.Refs)
	}
}

func TestExpandEnv_SameVar_ManyPaths_AllRecorded(t *testing.T) {
	env := map[string]string{"A": "${X}", "B": "${X}"}
	err := ExpandEnv(env, fakeLookup(nil))
	if err == nil {
		t.Fatal("expected error")
	}
	paths := err.Refs["X"]
	if len(paths) != 2 {
		t.Errorf("X should be referenced from 2 paths, got %v", paths)
	}
}

func TestExpandEnv_ErrorMessage_SortedAndFormatted(t *testing.T) {
	env := map[string]string{"A": "${ZED}", "B": "${ALPHA}"}
	err := ExpandEnv(env, fakeLookup(nil))
	if err == nil {
		t.Fatal("expected error")
	}
	msg := err.Error()
	// Sorted: ALPHA before ZED.
	if strings.Index(msg, "ALPHA") > strings.Index(msg, "ZED") {
		t.Errorf("error message not alphabetically sorted: %s", msg)
	}
	if !strings.Contains(msg, "[env].A") || !strings.Contains(msg, "[env].B") {
		t.Errorf("error message missing path attributions: %s", msg)
	}
}

func TestExpandEnv_NilEnv_NoOp(t *testing.T) {
	if err := ExpandEnv(nil, fakeLookup(nil)); err != nil {
		t.Errorf("nil env should be a no-op, got %v", err)
	}
}

func TestExpandEnv_EmptyEnv_NoOp(t *testing.T) {
	env := map[string]string{}
	if err := ExpandEnv(env, fakeLookup(nil)); err != nil {
		t.Errorf("empty env should be a no-op, got %v", err)
	}
}
