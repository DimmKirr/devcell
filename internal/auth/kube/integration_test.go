//go:build integration

// Integration test: brings up a real kube-apiserver + etcd via envtest,
// runs Bootstrap() against the admin kubeconfig it produces, then verifies
// the resulting -read kubeconfig grants reads and denies writes via the
// real `kubectl auth can-i` against the live RBAC enforcer.
//
// envtest is the conventional, docker-free way to test against a real K8s
// API server. It needs the `kube-apiserver` and `etcd` binaries on disk.
//
// Setup (one-time):
//
//	go install sigs.k8s.io/controller-runtime/tools/setup-envtest@latest
//	export KUBEBUILDER_ASSETS=$(setup-envtest use -p path)
//
// Run:
//
//	go test -tags=integration ./internal/auth/kube/...
//
// Skipped cleanly when KUBEBUILDER_ASSETS is empty or envtest can't start.

package kube

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"k8s.io/client-go/tools/clientcmd"
	clientcmdapi "k8s.io/client-go/tools/clientcmd/api"
	"sigs.k8s.io/controller-runtime/pkg/envtest"
)

// startEnvtest spins up apiserver + etcd. Returns the path to an admin
// kubeconfig and a teardown func. Skips the test if envtest binaries are
// not available.
func startEnvtest(t *testing.T) (kubeconfig string, teardown func()) {
	t.Helper()
	if os.Getenv("KUBEBUILDER_ASSETS") == "" {
		t.Skip("KUBEBUILDER_ASSETS not set — install with `setup-envtest use` (see file header)")
	}
	if _, err := exec.LookPath("kubectl"); err != nil {
		t.Skip("kubectl not on PATH — integration test needs the real kubectl")
	}

	env := &envtest.Environment{}
	cfg, err := env.Start()
	if err != nil {
		t.Skipf("envtest.Start failed (binaries may be missing): %v", err)
	}
	stopped := false
	teardown = func() {
		if stopped {
			return
		}
		stopped = true
		_ = env.Stop()
	}

	// Write a kubeconfig from the rest.Config so kubectl can talk to it.
	kcfg := clientcmdapi.NewConfig()
	kcfg.Clusters["envtest"] = &clientcmdapi.Cluster{
		Server:                   cfg.Host,
		CertificateAuthorityData: cfg.CAData,
	}
	kcfg.AuthInfos["admin"] = &clientcmdapi.AuthInfo{
		ClientCertificateData: cfg.CertData,
		ClientKeyData:         cfg.KeyData,
	}
	kcfg.Contexts["envtest"] = &clientcmdapi.Context{
		Cluster:  "envtest",
		AuthInfo: "admin",
	}
	kcfg.CurrentContext = "envtest"

	kubeconfig = filepath.Join(t.TempDir(), "admin")
	if err := clientcmd.WriteToFile(*kcfg, kubeconfig); err != nil {
		teardown()
		t.Fatalf("write kubeconfig: %v", err)
	}
	return kubeconfig, teardown
}

// TestIntegration_Bootstrap_EndToEnd: full real path — envtest API server,
// real RBAC, real `kubectl auth can-i`. The mocked execCommand from the
// unit-test file is NOT touched here; this test uses the real kubectl.
func TestIntegration_Bootstrap_EndToEnd(t *testing.T) {
	src, teardown := startEnvtest(t)
	defer teardown()

	// Restore the real kubectl (in case a prior test in this package set the mock).
	orig := execCommand
	t.Cleanup(func() { execCommand = orig })
	execCommand = exec.CommandContext

	out := filepath.Join(t.TempDir(), "admin-read")

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	var buf strings.Builder
	if err := Bootstrap(ctx, Options{
		Source: src, Output: out,
		SAName:    "integration-readonly",
		Namespace: "default",
		TTL:       1 * time.Hour, // envtest may reject longer durations
		Yes:       true,
	}, &buf); err != nil {
		t.Fatalf("Bootstrap end-to-end: %v\noutput so far:\n%s", err, buf.String())
	}

	// Independent verification: ignore Bootstrap's own verify step and
	// re-run `auth can-i` against the produced kubeconfig from scratch.
	envv := append(os.Environ(), "KUBECONFIG="+out)

	check := func(args ...string) string {
		cmd := exec.CommandContext(ctx, "kubectl", args...)
		cmd.Env = envv
		out, _ := cmd.Output() // can-i exits 1 on "no"; we only care about stdout
		return strings.TrimSpace(string(out))
	}
	if got := check("auth", "can-i", "list", "pods"); got != "yes" {
		t.Errorf("post-bootstrap: list pods = %q, want yes", got)
	}
	if got := check("auth", "can-i", "create", "pods"); got != "no" {
		t.Errorf("post-bootstrap: create pods = %q, want no", got)
	}
	if got := check("auth", "can-i", "delete", "secrets"); got != "no" {
		t.Errorf("post-bootstrap: delete secrets = %q, want no", got)
	}

	// File-mode safety: kubeconfig must be 0600 (token is inside).
	st, err := os.Stat(out)
	if err != nil {
		t.Fatalf("stat output: %v", err)
	}
	if mode := st.Mode().Perm(); mode != 0o600 {
		t.Errorf("output mode = %o, want 0600", mode)
	}
}

// TestIntegration_Bootstrap_Idempotent: re-running against the same cluster
// must succeed (SA + binding already exist, AlreadyExists swallowed, token
// freshly minted, kubeconfig overwritten).
func TestIntegration_Bootstrap_Idempotent(t *testing.T) {
	src, teardown := startEnvtest(t)
	defer teardown()

	orig := execCommand
	t.Cleanup(func() { execCommand = orig })
	execCommand = exec.CommandContext

	out := filepath.Join(t.TempDir(), "admin-read")
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()

	opts := Options{
		Source: src, Output: out,
		SAName:    "idempotent-readonly",
		Namespace: "default",
		TTL:       1 * time.Hour,
		Yes:       true,
	}

	var buf strings.Builder
	if err := Bootstrap(ctx, opts, &buf); err != nil {
		t.Fatalf("first run: %v", err)
	}
	buf.Reset()
	if err := Bootstrap(ctx, opts, &buf); err != nil {
		t.Fatalf("second run (idempotent): %v", err)
	}
}
