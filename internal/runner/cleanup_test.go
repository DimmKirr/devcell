package runner_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/DimmKirr/devcell/internal/runner"
)

// CELL-334: `cell cleanup` + prune preflight share one primitive — map
// running devcell containers to the volume-resident closures they depend
// on. Retention policy (decided 2026-08-01): "in use" means a RUNNING
// container (docker ps), not any existing one. Stopped cells' closures are
// reapable; recovery is the CELL-38 hydration-gate rebuild path.

func fakeResolve(m map[string]string) func(container, path string) (string, error) {
	return func(container, path string) (string, error) {
		v, ok := m[container+" "+path]
		if !ok {
			return "", errors.New("no such path")
		}
		return v, nil
	}
}

func TestCollectLiveClosures_ResolvesProfileAndGenerationPerContainer(t *testing.T) {
	ps := func() ([]string, error) { return []string{"cell-a", "cell-b"}, nil }
	resolve := fakeResolve(map[string]string{
		"cell-a " + runner.ContainerProfileLink:    "/nix/store/aaa111-home-manager-path",
		"cell-a " + runner.ContainerGenerationLink: "/nix/store/bbb222-home-manager-generation",
		"cell-b " + runner.ContainerProfileLink:    "/nix/store/ccc333-home-manager-path",
		"cell-b " + runner.ContainerGenerationLink: "/nix/store/ddd444-home-manager-generation",
	})

	closures, err := runner.CollectLiveClosures(ps, resolve, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(closures) != 2 {
		t.Fatalf("want 2 closures, got %d: %+v", len(closures), closures)
	}
	if closures[0].Container != "cell-a" ||
		closures[0].ProfilePath != "/nix/store/aaa111-home-manager-path" ||
		closures[0].GenerationPath != "/nix/store/bbb222-home-manager-generation" {
		t.Errorf("closure[0] wrong: %+v", closures[0])
	}
}

// A running container whose closure cannot be resolved must fail the whole
// collection — an unknown closure means neither the cleanup reaper nor the
// prune gate can guarantee safety, so both must refuse, naming the cell.
func TestCollectLiveClosures_UnresolvableContainerFailsNamingIt(t *testing.T) {
	ps := func() ([]string, error) { return []string{"cell-broken"}, nil }
	resolve := fakeResolve(map[string]string{})

	_, err := runner.CollectLiveClosures(ps, resolve, nil)
	if err == nil {
		t.Fatal("want error for unresolvable container, got nil")
	}
	if !strings.Contains(err.Error(), "cell-broken") {
		t.Errorf("error must name the container: %v", err)
	}
}

func TestCollectLiveClosures_NoRunningContainersIsEmptyNotError(t *testing.T) {
	ps := func() ([]string, error) { return nil, nil }
	closures, err := runner.CollectLiveClosures(ps, fakeResolve(nil), nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(closures) != 0 {
		t.Fatalf("want 0 closures, got %+v", closures)
	}
}

func TestCollectLiveClosures_LogsDatapoints(t *testing.T) {
	ps := func() ([]string, error) { return []string{"cell-a"}, nil }
	resolve := fakeResolve(map[string]string{
		"cell-a " + runner.ContainerProfileLink:    "/nix/store/aaa111-home-manager-path",
		"cell-a " + runner.ContainerGenerationLink: "/nix/store/bbb222-home-manager-generation",
	})
	var lines []string
	logf := func(format string, args ...any) {
		lines = append(lines, format)
	}
	if _, err := runner.CollectLiveClosures(ps, resolve, logf); err != nil {
		t.Fatal(err)
	}
	if len(lines) == 0 {
		t.Error("CollectLiveClosures must log datapoints via logf for --debug visibility")
	}
}

// StampRootsScript makes the prune-gate invariant true by construction:
// instead of refusing when a running container has no GC root, stamp the
// (idempotent, hash-named) roots for every live closure before GC runs.
func TestStampRootsScript_StampsHashNamedRootsForEveryLiveClosure(t *testing.T) {
	closures := []runner.LiveClosure{
		{
			Container:      "cell-a",
			ProfilePath:    "/nix/store/aaa111-home-manager-path",
			GenerationPath: "/nix/store/bbb222-home-manager-generation",
		},
	}
	script := runner.StampRootsScript(closures)

	wants := []string{
		"mkdir -p /nix/var/nix/gcroots/devcell",
		`ln -sfT "/nix/store/aaa111-home-manager-path" /nix/var/nix/gcroots/devcell/aaa111-profile`,
		`ln -sfT "/nix/store/bbb222-home-manager-generation" /nix/var/nix/gcroots/devcell/bbb222-generation`,
	}
	for _, w := range wants {
		if !strings.Contains(script, w) {
			t.Errorf("stamp script missing %q\nscript:\n%s", w, script)
		}
	}
	if strings.Contains(script, "rm ") || strings.Contains(script, "nix-store") {
		t.Errorf("stamp script must only create roots, never delete or GC:\n%s", script)
	}
}

func TestStampRootsScript_EmptyClosuresIsEmpty(t *testing.T) {
	if s := runner.StampRootsScript(nil); s != "" {
		t.Errorf("no live closures → empty stamp script, got %q", s)
	}
}

// The reaper: remove devcell/ roots whose hash no RUNNING container's
// closure matches. Only touches /nix/var/nix/gcroots/devcell/ — never
// auto/ roots (namespace-local, CELL-330), never the store itself.
func TestBuildCleanupScript_KeepsLiveReapsStale(t *testing.T) {
	closures := []runner.LiveClosure{
		{
			Container:      "cell-a",
			ProfilePath:    "/nix/store/aaa111-home-manager-path",
			GenerationPath: "/nix/store/bbb222-home-manager-generation",
		},
	}
	script := runner.BuildCleanupScript(closures)

	if !strings.Contains(script, `LIVE=" aaa111 bbb222 "`) {
		t.Errorf("cleanup script must embed the live hash set, got:\n%s", script)
	}
	if !strings.Contains(script, "/nix/var/nix/gcroots/devcell/") {
		t.Errorf("cleanup script must scan gcroots/devcell/:\n%s", script)
	}
	if strings.Contains(script, "gcroots/auto") {
		t.Errorf("cleanup script must never touch auto/ roots (CELL-330):\n%s", script)
	}
	for _, forbidden := range []string{"nix-store", "nix-collect-garbage", "rm -rf /nix/store"} {
		if strings.Contains(script, forbidden) {
			t.Errorf("cleanup script must only reap root symlinks, not GC (%q found):\n%s", forbidden, script)
		}
	}
}

// Running-only semantics: with nothing running, every root is reapable.
// The script must still be valid (empty LIVE set) — `cell cleanup` is
// explicit and confirmation-gated, so this is intended, not a foot-gun.
func TestBuildCleanupScript_NoLiveClosuresReapsAll(t *testing.T) {
	script := runner.BuildCleanupScript(nil)
	if !strings.Contains(script, `LIVE="  "`) && !strings.Contains(script, `LIVE=" "`) {
		t.Errorf("empty live set must produce an empty LIVE list:\n%s", script)
	}
}

func TestBuildCleanupSteps_RunsInContainerOnNixVolume(t *testing.T) {
	steps := runner.BuildCleanupSteps(nil)
	if len(steps) != 1 {
		t.Fatalf("want 1 step, got %d: %+v", len(steps), steps)
	}
	joined := strings.Join(steps[0].Argv, " ")
	if !strings.Contains(joined, "docker run") ||
		!strings.Contains(joined, runner.DefaultThinStoreVolume+":/nix") {
		t.Errorf("cleanup must run in a container mounting %s:/nix, got: %v",
			runner.DefaultThinStoreVolume, steps[0].Argv)
	}
}

// RunCleanup orchestrates: build the reaper plan, prompt, execute. Same
// confirmation semantics as RunPrune — rejection is a clean no-op, non-TTY
// without --yes refuses.
func TestRunCleanup_RejectedPromptExecutesNothing(t *testing.T) {
	executed := 0
	var out strings.Builder
	err := runner.RunCleanup(runner.RunCleanupArgs{
		Closures: nil,
		Exec:     func(runner.PruneStep) error { executed++; return nil },
		Out:      &out,
		In:       strings.NewReader("n\n"),
		IsTTY:    true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if executed != 0 {
		t.Errorf("rejected prompt must execute nothing, executed %d step(s)", executed)
	}
}

func TestRunCleanup_AcceptedPromptExecutesReaperStep(t *testing.T) {
	var got []runner.PruneStep
	var out strings.Builder
	err := runner.RunCleanup(runner.RunCleanupArgs{
		Closures: []runner.LiveClosure{
			{
				Container:      "cell-a",
				ProfilePath:    "/nix/store/aaa111-home-manager-path",
				GenerationPath: "/nix/store/bbb222-home-manager-generation",
			},
		},
		Exec:  func(s runner.PruneStep) error { got = append(got, s); return nil },
		Out:   &out,
		In:    strings.NewReader("y\n"),
		IsTTY: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 {
		t.Fatalf("want 1 executed step, got %d", len(got))
	}
	joined := strings.Join(got[0].Argv, " ")
	if !strings.Contains(joined, runner.DefaultThinStoreVolume+":/nix") {
		t.Errorf("executed step must mount the nix volume: %v", got[0].Argv)
	}
}

// The prompt must tell the user the retention rule and how many containers
// are protected — this is the primary non-debug UX for cleanup.
func TestBuildCleanupPrompt_NamesRetentionRuleAndLiveCount(t *testing.T) {
	closures := []runner.LiveClosure{
		{Container: "cell-a", ProfilePath: "/nix/store/aaa111-p", GenerationPath: "/nix/store/bbb222-g"},
		{Container: "cell-b", ProfilePath: "/nix/store/aaa111-p", GenerationPath: "/nix/store/bbb222-g"},
	}
	prompt := runner.BuildCleanupPrompt(closures)
	for _, want := range []string{"running", "2 running container", "Continue? [y/N]"} {
		if !strings.Contains(prompt, want) {
			t.Errorf("cleanup prompt missing %q\nprompt:\n%s", want, prompt)
		}
	}
}

func TestBuildCleanupPrompt_WarnsWhenNothingRunning(t *testing.T) {
	prompt := runner.BuildCleanupPrompt(nil)
	if !strings.Contains(prompt, "ALL") {
		t.Errorf("with nothing running the prompt must warn that ALL roots are reaped:\n%s", prompt)
	}
}

// CELL-334 secondary: PROTECTED must be collected from BOTH *-profile and
// *-generation roots — generations carry home-manager-files, the very
// closure CELL-320 exists to protect.
func TestSafeNixGCScript_CollectsProtectedFromBothRootKinds(t *testing.T) {
	if !strings.Contains(runner.SafeNixGCScript,
		"/nix/var/nix/gcroots/devcell/*-profile /nix/var/nix/gcroots/devcell/*-generation") {
		t.Error("SafeNixGCScript must collect PROTECTED from both *-profile and *-generation globs (CELL-334)")
	}
}

// The prune gate: when live closures are supplied, the plan must stamp
// roots for them (making "running implies rooted" true) before the safe GC
// step. Order matters — stamp, then GC.
func TestBuildNixPruneSteps_LiveClosuresInsertStampStepBeforeGC(t *testing.T) {
	closures := []runner.LiveClosure{
		{
			Container:      "cell-a",
			ProfilePath:    "/nix/store/aaa111-home-manager-path",
			GenerationPath: "/nix/store/bbb222-home-manager-generation",
		},
	}
	opts := runner.PruneOpts{GOOS: "linux", Pure: true, LiveClosures: closures}
	steps := runner.BuildNixPruneSteps(opts)

	stampIdx, gcIdx := -1, -1
	for i, s := range steps {
		joined := strings.Join(s.Argv, " ")
		if strings.Contains(joined, "aaa111-profile") {
			stampIdx = i
		}
		if strings.Contains(joined, "nix-store --gc") {
			gcIdx = i
		}
	}
	if stampIdx == -1 {
		t.Fatalf("no stamp step found in plan: %+v", steps)
	}
	if gcIdx == -1 {
		t.Fatalf("no safe GC step found in plan: %+v", steps)
	}
	if stampIdx >= gcIdx {
		t.Errorf("stamp step (%d) must run before safe GC (%d)", stampIdx, gcIdx)
	}
}
