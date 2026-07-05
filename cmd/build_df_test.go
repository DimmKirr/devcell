package main

import "testing"

// `cell build df` flag defaults — pin these so the user-visible interface
// can't drift without a failing test. Mirror the table from CELL-98 spec.
func TestDfCmd_TopFlag_DefaultsTo10(t *testing.T) {
	got, err := dfCmd.Flags().GetInt("top")
	if err != nil {
		t.Fatalf("GetInt(top): %v", err)
	}
	if got != 10 {
		t.Errorf("default --top = %d, want 10", got)
	}
}

func TestDfCmd_JsonFlag_Exists(t *testing.T) {
	if dfCmd.Flag("json") == nil {
		t.Error("--json flag missing")
	}
}

func TestDfCmd_AllFlag_Exists(t *testing.T) {
	if dfCmd.Flag("all") == nil {
		t.Error("--all flag missing")
	}
}

func TestDfCmd_KindFlag_AcceptsMultipleValues(t *testing.T) {
	f := dfCmd.Flag("kind")
	if f == nil {
		t.Fatal("--kind flag missing")
	}
	// StringSlice prints as "[]" when empty — confirm slice semantics.
	if f.Value.Type() != "stringSlice" {
		t.Errorf("--kind type = %q, want stringSlice", f.Value.Type())
	}
}

// The --kind flag help text says "images, containers, volumes, cache"
// (mostly plural) but internal EntryKind constants are singular ("image",
// "container", "volume", "cache"). The cmd-layer mapper must accept both
// so users can type either form without quietly getting empty results.
// Regression from smoke run 2026-05-23: `--kind volumes --kind images`
// returned zero rows.
func TestToEntryKinds_AcceptsBothSingularAndPlural(t *testing.T) {
	cases := []struct {
		in   []string
		want []string
	}{
		{[]string{"image"}, []string{"image"}},
		{[]string{"images"}, []string{"image"}},
		{[]string{"volumes", "cache"}, []string{"volume", "cache"}},
		{[]string{"container", "containers"}, []string{"container", "container"}},
	}
	for _, c := range cases {
		got := toEntryKinds(c.in)
		if len(got) != len(c.want) {
			t.Errorf("toEntryKinds(%v): len = %d, want %d", c.in, len(got), len(c.want))
			continue
		}
		for i, w := range c.want {
			if string(got[i]) != w {
				t.Errorf("toEntryKinds(%v)[%d] = %q, want %q", c.in, i, got[i], w)
			}
		}
	}
}

// dfCmd must be registered as a subcommand of buildCmd, alongside `prune`,
// so users run it as `cell build df` (not as a top-level command).
func TestDfCmd_IsRegisteredUnderBuild(t *testing.T) {
	found := false
	for _, sub := range buildCmd.Commands() {
		if sub.Use == "df" {
			found = true
			break
		}
	}
	if !found {
		t.Error("dfCmd not registered as subcommand of buildCmd")
	}
}
