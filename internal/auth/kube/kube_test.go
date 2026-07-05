package kube

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// fakeKubectl returns an exec.CommandContext substitute that runs `sh -c`
// against a per-invocation script. The harness records each invocation
// (args slice) so tests can assert on the call sequence.
type fakeKubectl struct {
	calls   [][]string
	scripts []string // shell script per call, in order; missing → echo "" exit 0
}

func (f *fakeKubectl) command(ctx context.Context, name string, args ...string) *exec.Cmd {
	idx := len(f.calls)
	f.calls = append(f.calls, append([]string{name}, args...))
	script := `echo ""`
	if idx < len(f.scripts) {
		script = f.scripts[idx]
	}
	return exec.CommandContext(ctx, "sh", "-c", script)
}

func setupExec(t *testing.T, scripts ...string) *fakeKubectl {
	t.Helper()
	orig := execCommand
	t.Cleanup(func() { execCommand = orig })
	f := &fakeKubectl{scripts: scripts}
	execCommand = f.command
	return f
}

// stubKubectl pretends kubectl exists on PATH by creating a no-op binary
// next to the test and prepending that dir to PATH for the test's duration.
// Bootstrap does an exec.LookPath("kubectl") sanity check before any work.
func stubKubectl(t *testing.T) {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "kubectl"), []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
}

func tempKubeconfig(t *testing.T) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "admin")
	if err := os.WriteFile(p, []byte("apiVersion: v1\nkind: Config\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

// ---- Defaults --------------------------------------------------------------

func TestDefaults_ComputesAllFields(t *testing.T) {
	t.Setenv("KUBECONFIG", "")
	t.Setenv("USER", "alice")
	home := t.TempDir()
	t.Setenv("HOME", home)

	got := Defaults(Options{})
	wantSrc := filepath.Join(home, ".kube", "config")
	if got.Source != wantSrc {
		t.Errorf("Source = %q, want %q", got.Source, wantSrc)
	}
	if got.Output != wantSrc+"-read" {
		t.Errorf("Output = %q, want %q", got.Output, wantSrc+"-read")
	}
	if got.SAName != "alice-readonly" {
		t.Errorf("SAName = %q, want %q", got.SAName, "alice-readonly")
	}
	if got.Namespace != "default" {
		t.Errorf("Namespace = %q, want default", got.Namespace)
	}
	if got.TTL != 8760*time.Hour {
		t.Errorf("TTL = %v, want 8760h", got.TTL)
	}
}

func TestDefaults_KubeconfigEnvWins(t *testing.T) {
	t.Setenv("KUBECONFIG", "/explicit/path")
	t.Setenv("USER", "bob")
	got := Defaults(Options{})
	if got.Source != "/explicit/path" {
		t.Errorf("Source = %q, want /explicit/path", got.Source)
	}
	if got.Output != "/explicit/path-read" {
		t.Errorf("Output = %q, want /explicit/path-read", got.Output)
	}
}

func TestDefaults_KubeconfigEnvList_PicksFirst(t *testing.T) {
	t.Setenv("KUBECONFIG", "/a"+string(os.PathListSeparator)+"/b")
	got := Defaults(Options{})
	if got.Source != "/a" {
		t.Errorf("Source = %q, want /a (first of list)", got.Source)
	}
}

func TestDefaults_UserOverride_NotClobbered(t *testing.T) {
	t.Setenv("USER", "alice")
	got := Defaults(Options{SAName: "custom-name"})
	if got.SAName != "custom-name" {
		t.Errorf("SAName = %q, want custom-name (explicit value must not be overwritten)", got.SAName)
	}
}

// ---- Bootstrap: declined prompt aborts cleanly -----------------------------

func TestBootstrap_DeclinedPrompt_NoClusterCalls_NoFile(t *testing.T) {
	stubKubectl(t)
	src := tempKubeconfig(t)
	out := filepath.Join(t.TempDir(), "out")

	// 3 inspectSource calls before the prompt — provide outputs.
	f := setupExec(t,
		`echo nmd-prod`,                       // current-context
		`echo prod-cluster`,                   // cluster name
		`echo https://k.example:6443`,         // server
		`echo admin@nmd-prod`,                 // identity
	)

	orig := confirmFn
	t.Cleanup(func() { confirmFn = orig })
	confirmFn = func(ctx context.Context, _ string) (bool, error) { return false, nil }

	var buf bytes.Buffer
	err := Bootstrap(context.Background(), Options{
		Source: src, Output: out, SAName: "alice-readonly", Yes: false,
	}, &buf)
	if err != nil {
		t.Fatalf("expected nil err on decline, got %v", err)
	}
	if _, statErr := os.Stat(out); !os.IsNotExist(statErr) {
		t.Errorf("output kubeconfig should not exist on decline (stat err: %v)", statErr)
	}
	// Only the 4 inspect calls — no create/token/set-credentials.
	if got := len(f.calls); got != 4 {
		t.Errorf("kubectl call count = %d, want 4 (inspect only); calls: %v", got, f.calls)
	}
	if !strings.Contains(buf.String(), "Aborted") {
		t.Errorf("output should say Aborted, got: %s", buf.String())
	}
}

// ---- Bootstrap: --skip-cluster skips create-sa/binding/token ----------------

func TestBootstrap_SkipCluster_NoCreateCalls(t *testing.T) {
	stubKubectl(t)
	src := tempKubeconfig(t)
	out := filepath.Join(t.TempDir(), "out")

	// Need: 4 inspect + 1 token + 2 config (set-credentials, set-context)
	//       + 2 verify (can-i list pods, can-i create pods)
	f := setupExec(t,
		`echo nmd-prod`,                 // current-context
		`echo prod-cluster`,             // cluster
		`echo https://k.example:6443`,   // server
		`echo admin@nmd-prod`,           // identity
		`echo TOKEN_BLOB`,               // token
		`echo ""`,                       // set-credentials
		`echo ""`,                       // set-context
		`echo yes`,                      // can-i list pods
		`echo no; exit 1`,               // can-i create pods (exits 1 on no)
	)

	var buf bytes.Buffer
	if err := Bootstrap(context.Background(), Options{
		Source: src, Output: out, SAName: "alice-readonly",
		SkipCluster: true, Yes: true,
	}, &buf); err != nil {
		t.Fatalf("Bootstrap: %v", err)
	}

	// Inspect every call: none may be `create sa` or `create clusterrolebinding`.
	for _, c := range f.calls {
		joined := strings.Join(c, " ")
		if strings.Contains(joined, "create sa ") || strings.Contains(joined, "create clusterrolebinding") {
			t.Errorf("--skip-cluster must not call %q", joined)
		}
	}
}

// ---- Bootstrap: AlreadyExists on SA/binding is swallowed -------------------

func TestBootstrap_AlreadyExists_Swallowed(t *testing.T) {
	stubKubectl(t)
	src := tempKubeconfig(t)
	out := filepath.Join(t.TempDir(), "out")

	setupExec(t,
		`echo nmd-prod`,                                                                // current-context
		`echo prod-cluster`,                                                            // cluster
		`echo https://k.example:6443`,                                                  // server
		`echo admin@nmd-prod`,                                                          // identity
		`echo 'Error from server (AlreadyExists): serviceaccounts "x" already exists' >&2; exit 1`, // create sa
		`echo 'Error from server (AlreadyExists): clusterrolebindings.rbac.authorization.k8s.io "x" already exists' >&2; exit 1`, // crb
		`echo TOKEN_BLOB`,            // token
		`echo ""`,                    // set-credentials
		`echo ""`,                    // set-context
		`echo yes`,                   // can-i list pods
		`echo no; exit 1`,            // can-i create pods
	)

	var buf bytes.Buffer
	if err := Bootstrap(context.Background(), Options{
		Source: src, Output: out, SAName: "alice-readonly", Yes: true,
	}, &buf); err != nil {
		t.Fatalf("AlreadyExists must be swallowed, got: %v", err)
	}
}

// ---- Bootstrap: verify reads-no fails loudly --------------------------------

func TestBootstrap_VerifyReadsNo_FailsLoudly(t *testing.T) {
	stubKubectl(t)
	src := tempKubeconfig(t)
	out := filepath.Join(t.TempDir(), "out")

	setupExec(t,
		`echo nmd-prod`,
		`echo prod-cluster`,
		`echo https://k.example:6443`,
		`echo admin@nmd-prod`,
		`echo ""`,         // create sa
		`echo ""`,         // crb
		`echo TOKEN_BLOB`, // token
		`echo ""`,         // set-credentials
		`echo ""`,         // set-context
		`echo no; exit 1`, // can-i list pods → no (BAD)
	)

	var buf bytes.Buffer
	err := Bootstrap(context.Background(), Options{
		Source: src, Output: out, SAName: "alice-readonly", Yes: true,
	}, &buf)
	if err == nil {
		t.Fatal("expected verify error when reads=no")
	}
	if !strings.Contains(err.Error(), "read check failed") {
		t.Errorf("err should mention read check: %v", err)
	}
}

// ---- Bootstrap: verify writes-yes fails loudly ------------------------------

func TestBootstrap_VerifyWritesYes_FailsLoudly(t *testing.T) {
	stubKubectl(t)
	src := tempKubeconfig(t)
	out := filepath.Join(t.TempDir(), "out")

	setupExec(t,
		`echo nmd-prod`,
		`echo prod-cluster`,
		`echo https://k.example:6443`,
		`echo admin@nmd-prod`,
		`echo ""`,
		`echo ""`,
		`echo TOKEN_BLOB`,
		`echo ""`,
		`echo ""`,
		`echo yes`, // reads = yes
		`echo yes`, // writes = yes  ← BAD; binding too broad
	)

	var buf bytes.Buffer
	err := Bootstrap(context.Background(), Options{
		Source: src, Output: out, SAName: "alice-readonly", Yes: true,
	}, &buf)
	if err == nil {
		t.Fatal("expected verify error when writes=yes")
	}
	if !strings.Contains(err.Error(), "write check failed") {
		t.Errorf("err should mention write check: %v", err)
	}
}

// ---- Bootstrap: success path writes kubeconfig and prints TOML snippet -----

func TestBootstrap_Success_WritesFile_PrintsTOML(t *testing.T) {
	stubKubectl(t)
	src := tempKubeconfig(t)
	out := filepath.Join(t.TempDir(), "out")

	setupExec(t,
		`echo nmd-prod`,
		`echo prod-cluster`,
		`echo https://k.example:6443`,
		`echo admin@nmd-prod`,
		`echo ""`,         // create sa
		`echo ""`,         // crb
		`echo TOKEN_BLOB`, // token
		`echo ""`,         // set-credentials
		`echo ""`,         // set-context
		`echo yes`,        // can-i list pods
		`echo no; exit 1`, // can-i create pods
	)

	var buf bytes.Buffer
	if err := Bootstrap(context.Background(), Options{
		Source: src, Output: out, SAName: "alice-readonly", Yes: true,
	}, &buf); err != nil {
		t.Fatalf("Bootstrap: %v", err)
	}

	// File written with 0600.
	st, err := os.Stat(out)
	if err != nil {
		t.Fatalf("output not written: %v", err)
	}
	if mode := st.Mode().Perm(); mode != 0o600 {
		t.Errorf("output mode = %o, want 0600", mode)
	}

	// TOML snippet present and includes [[volumes]] + [env] KUBECONFIG=.
	s := buf.String()
	if !strings.Contains(s, "[[volumes]]") {
		t.Errorf("output should contain [[volumes]] block: %s", s)
	}
	if !strings.Contains(s, `KUBECONFIG = "`) {
		t.Errorf("output should contain KUBECONFIG line: %s", s)
	}
	if !strings.Contains(s, ":ro\"") {
		t.Errorf("mount should end with :ro\": %s", s)
	}
	// Mount must be file→file (NOT parent-dir, which would leak the admin
	// kubeconfig sitting next to it).
	if !strings.Contains(s, out+":") {
		t.Errorf("mount source should be the output kubeconfig file %q, got: %s", out, s)
	}
	if strings.Contains(s, filepath.Dir(out)+":") {
		t.Errorf("mount must NOT bind the parent dir (would leak admin kubeconfig): %s", s)
	}
	// Container side must mirror host path (same convention as runner.go's
	// project bind mount); KUBECONFIG must point at the same path. NOT a
	// translated /home/<user>/... path.
	if !strings.Contains(s, `mount = "`+out+":"+out+`:ro"`) {
		t.Errorf("mount should mirror host path on container side, got: %s", s)
	}
	if !strings.Contains(s, `KUBECONFIG = "`+out+`"`) {
		t.Errorf("KUBECONFIG should equal host path %q, got: %s", out, s)
	}
}

// ---- Bootstrap: missing kubectl produces actionable error ------------------

func TestBootstrap_KubectlMissing_ActionableError(t *testing.T) {
	// Empty PATH so LookPath fails.
	t.Setenv("PATH", "")
	src := tempKubeconfig(t)

	var buf bytes.Buffer
	err := Bootstrap(context.Background(), Options{
		Source: src, Output: src + "-read", SAName: "alice-readonly", Yes: true,
	}, &buf)
	if err == nil {
		t.Fatal("expected error when kubectl missing")
	}
	if !strings.Contains(err.Error(), "kubectl not found") {
		t.Errorf("err should be actionable about kubectl: %v", err)
	}
}

// ---- Bootstrap: missing source kubeconfig --------------------------------

func TestBootstrap_MissingSource_ReturnsError(t *testing.T) {
	stubKubectl(t)
	var buf bytes.Buffer
	err := Bootstrap(context.Background(), Options{
		Source: "/no/such/kubeconfig", Output: "/tmp/out", SAName: "x", Yes: true,
	}, &buf)
	if err == nil {
		t.Fatal("expected error for missing source")
	}
	if !strings.Contains(err.Error(), "source kubeconfig") {
		t.Errorf("err should mention source kubeconfig: %v", err)
	}
}
