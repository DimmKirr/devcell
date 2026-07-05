// Package kube bootstraps a read-only kubeconfig for the cell.
//
// Flow: take an admin kubeconfig on the host, create a ServiceAccount bound
// to the built-in `view` ClusterRole on the cluster, mint a token, and write
// a sibling kubeconfig authenticated as that SA. The cell mounts the sibling
// and gets cluster reads with server-enforced no-writes — regardless of which
// tool inside the cell uses it (kubectl, helm, the kubernetes-mcp-server).
//
// All cluster mutation happens via host `kubectl`. No client-go dependency.
package kube

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"strings"
	"time"

	"github.com/DimmKirr/devcell/internal/ux"
)

// okMark / failMark render styled prefixes that match the rest of devcell's
// output (✓ green / ✗ red, falling back to plain in non-color terminals).
func okMark() string   { return ux.StyleSuccess.Render("✓") }
func failMark() string { return ux.StyleError.Render("✗") }

// Options controls Bootstrap. Zero-value Options should be passed through
// Defaults() before use.
type Options struct {
	Source      string        // path to admin kubeconfig
	Output      string        // path to write the -read sibling
	SAName      string        // ServiceAccount name on the cluster
	Namespace   string        // ServiceAccount namespace
	TTL         time.Duration // requested token duration (cluster may cap)
	SkipCluster bool          // skip SA/binding/token creation (kubeconfig writeback only)
	Yes         bool          // skip the interactive confirmation prompt
}

// Defaults fills in zero fields from the host environment.
//
//   - Source:    $KUBECONFIG, else ~/.kube/config
//   - Output:    derived from Source: `nmd-prod` → `nmd-prod-read`, `config` → `config-read`
//   - SAName:    $USER + "-readonly"  (e.g. dmitry-readonly)
//   - Namespace: "default"
//   - TTL:       8760h (1 year; cluster may cap shorter)
func Defaults(o Options) Options {
	if o.Source == "" {
		if v := os.Getenv("KUBECONFIG"); v != "" {
			o.Source = firstPath(v)
		} else if home, err := os.UserHomeDir(); err == nil {
			o.Source = filepath.Join(home, ".kube", "config")
		}
	}
	if o.Output == "" && o.Source != "" {
		o.Output = o.Source + "-read"
	}
	if o.SAName == "" {
		o.SAName = currentUser() + "-readonly"
	}
	if o.Namespace == "" {
		o.Namespace = "default"
	}
	if o.TTL == 0 {
		o.TTL = 8760 * time.Hour
	}
	return o
}

// firstPath returns the first path in a KUBECONFIG-style list ("a:b:c").
func firstPath(s string) string {
	if i := strings.IndexByte(s, os.PathListSeparator); i >= 0 {
		return s[:i]
	}
	return s
}

func currentUser() string {
	if u := os.Getenv("USER"); u != "" {
		return u
	}
	if u, err := user.Current(); err == nil && u.Username != "" {
		return u.Username
	}
	return "cell"
}

// execCommand is the indirection that tests override to mock kubectl.
// Production = exec.CommandContext.
var execCommand = exec.CommandContext

// confirmFn prompts the user for Y/n and returns the choice. Tests override.
// Production prompts via stdin (wired by the cobra layer via SetConfirmFn).
var confirmFn = func(ctx context.Context, msg string) (bool, error) {
	return false, errors.New("confirmFn not wired (call SetConfirmFn from the cobra layer)")
}

// SetConfirmFn wires the interactive prompt. The cobra layer calls this once
// at startup, routing through ux.GetConfirmation. Tests override `confirmFn`
// directly via the package-private var.
func SetConfirmFn(fn func(ctx context.Context, msg string) (bool, error)) {
	confirmFn = fn
}

// SourceInfo summarises the source kubeconfig for the confirmation prompt.
type SourceInfo struct {
	Context  string
	Server   string
	Identity string
}

// Bootstrap runs the full read-only kubeconfig flow. Status lines are
// written to out. Returns nil on success.
func Bootstrap(ctx context.Context, opts Options, out io.Writer) error {
	opts = Defaults(opts)

	if opts.Source == "" {
		return errors.New("no source kubeconfig (set $KUBECONFIG or ~/.kube/config)")
	}
	if _, err := os.Stat(opts.Source); err != nil {
		return fmt.Errorf("source kubeconfig: %w", err)
	}
	if _, err := exec.LookPath("kubectl"); err != nil {
		return errors.New("kubectl not found on PATH — install kubectl on the host before running `cell auth kube`")
	}

	info, err := inspectSource(ctx, opts.Source)
	if err != nil {
		return fmt.Errorf("inspect source kubeconfig: %w", err)
	}

	fmt.Fprintf(out, "\ncell auth kube — bootstrap a read-only kubeconfig for devcell\n\n")
	fmt.Fprintf(out, "  Source kubeconfig:   %s\n", opts.Source)
	fmt.Fprintf(out, "  Current context:     %s\n", info.Context)
	fmt.Fprintf(out, "  Cluster server:      %s\n", info.Server)
	fmt.Fprintf(out, "  Current identity:    %s\n\n", info.Identity)

	if opts.SkipCluster {
		fmt.Fprintf(out, "Mode: --skip-cluster (no SA/binding/token will be created)\n")
	} else {
		fmt.Fprintf(out, "On the cluster (existing resources are kept; only the token is freshly minted):\n")
		fmt.Fprintf(out, "  • ServiceAccount         %s/%s            (created if missing)\n", opts.Namespace, opts.SAName)
		fmt.Fprintf(out, "  • ClusterRoleBinding     %s → view  (created if missing)\n", opts.SAName)
		fmt.Fprintf(out, "  • Token                  fresh, duration: %s (cluster may cap shorter)\n\n", opts.TTL)
	}
	fmt.Fprintf(out, "Output kubeconfig:     %s\n\n", opts.Output)

	if !opts.Yes {
		ok, err := confirmFn(ctx, "Proceed?")
		if err != nil {
			return err
		}
		if !ok {
			fmt.Fprintf(out, "%s Aborted.\n", failMark())
			return nil
		}
	}

	if !opts.SkipCluster {
		if err := ensureSAandBinding(ctx, opts); err != nil {
			return err
		}
		fmt.Fprintf(out, "%s Cluster mutated:    SA %s/%s bound to `view` ClusterRole\n", okMark(), opts.Namespace, opts.SAName)
	}

	token, err := mintToken(ctx, opts)
	if err != nil {
		return fmt.Errorf("mint token: %w", err)
	}
	fmt.Fprintf(out, "%s Token minted:       requested %s\n", okMark(), opts.TTL)

	if err := writeKubeconfig(ctx, opts, token); err != nil {
		return fmt.Errorf("write kubeconfig: %w", err)
	}
	fmt.Fprintf(out, "%s Kubeconfig written: %s (mode 600)\n", okMark(), opts.Output)

	if err := verify(ctx, opts); err != nil {
		return fmt.Errorf("verify: %w", err)
	}
	fmt.Fprintf(out, "%s Verified:           list pods=yes, create pods=no\n\n", okMark())

	printTOMLSnippet(out, opts)
	return nil
}

// inspectSource pulls current-context, cluster.server, and the user.name
// from the source kubeconfig via `kubectl config view`.
func inspectSource(ctx context.Context, source string) (SourceInfo, error) {
	env := append(os.Environ(), "KUBECONFIG="+source)

	curCtx, err := runKubectlEnv(ctx, env, "config", "current-context")
	if err != nil {
		return SourceInfo{}, fmt.Errorf("no current-context: %w", err)
	}
	curCtx = strings.TrimSpace(curCtx)

	cluster, err := runKubectlEnv(ctx, env,
		"config", "view", "-o",
		"jsonpath={.contexts[?(@.name==\""+curCtx+"\")].context.cluster}")
	if err != nil {
		return SourceInfo{}, err
	}
	server, err := runKubectlEnv(ctx, env,
		"config", "view", "-o",
		"jsonpath={.clusters[?(@.name==\""+strings.TrimSpace(cluster)+"\")].cluster.server}")
	if err != nil {
		return SourceInfo{}, err
	}
	identity, err := runKubectlEnv(ctx, env,
		"config", "view", "-o",
		"jsonpath={.contexts[?(@.name==\""+curCtx+"\")].context.user}")
	if err != nil {
		return SourceInfo{}, err
	}

	return SourceInfo{
		Context:  curCtx,
		Server:   strings.TrimSpace(server),
		Identity: strings.TrimSpace(identity),
	}, nil
}

func ensureSAandBinding(ctx context.Context, opts Options) error {
	env := append(os.Environ(), "KUBECONFIG="+opts.Source)

	if _, err := runKubectlEnv(ctx, env,
		"-n", opts.Namespace, "create", "sa", opts.SAName,
	); err != nil && !isAlreadyExists(err) {
		return fmt.Errorf("create sa: %w", err)
	}
	if _, err := runKubectlEnv(ctx, env,
		"create", "clusterrolebinding", opts.SAName,
		"--clusterrole=view",
		"--serviceaccount="+opts.Namespace+":"+opts.SAName,
	); err != nil && !isAlreadyExists(err) {
		return fmt.Errorf("create clusterrolebinding: %w", err)
	}
	return nil
}

func mintToken(ctx context.Context, opts Options) (string, error) {
	env := append(os.Environ(), "KUBECONFIG="+opts.Source)
	out, err := runKubectlEnv(ctx, env,
		"-n", opts.Namespace, "create", "token", opts.SAName,
		"--duration="+opts.TTL.String(),
	)
	if err != nil {
		return "", err
	}
	tok := strings.TrimSpace(out)
	if tok == "" {
		return "", errors.New("kubectl create token returned empty output")
	}
	return tok, nil
}

func writeKubeconfig(ctx context.Context, opts Options, token string) error {
	src, err := os.ReadFile(opts.Source)
	if err != nil {
		return err
	}
	if err := os.WriteFile(opts.Output, src, 0o600); err != nil {
		return err
	}

	env := append(os.Environ(), "KUBECONFIG="+opts.Output)
	if _, err := runKubectlEnv(ctx, env,
		"config", "set-credentials", opts.SAName, "--token="+token,
	); err != nil {
		return fmt.Errorf("set-credentials: %w", err)
	}
	if _, err := runKubectlEnv(ctx, env,
		"config", "set-context", "--current", "--user="+opts.SAName,
	); err != nil {
		return fmt.Errorf("set-context: %w", err)
	}
	return nil
}

func verify(ctx context.Context, opts Options) error {
	env := append(os.Environ(), "KUBECONFIG="+opts.Output)

	reads, err := runKubectlEnv(ctx, env, "auth", "can-i", "list", "pods")
	if err != nil {
		// `can-i` exits 1 when the answer is "no" — surface stdout for clarity.
		reads = strings.TrimSpace(reads)
		if reads != "no" {
			return fmt.Errorf("can-i list pods: %w", err)
		}
	}
	if strings.TrimSpace(reads) != "yes" {
		return fmt.Errorf("read check failed: `kubectl auth can-i list pods` = %q (expected \"yes\")", strings.TrimSpace(reads))
	}

	writes, err := runKubectlEnv(ctx, env, "auth", "can-i", "create", "pods")
	if err == nil && strings.TrimSpace(writes) == "yes" {
		return errors.New("write check failed: `kubectl auth can-i create pods` = \"yes\" — the SA binding is too broad")
	}
	// "no" exits 1 — that's the success path here. Anything else is a bug.
	if w := strings.TrimSpace(writes); w != "" && w != "no" {
		return fmt.Errorf("write check unexpected: `kubectl auth can-i create pods` = %q", w)
	}
	return nil
}

func printTOMLSnippet(out io.Writer, opts Options) {
	// Mirror the host path inside the container — same convention devcell
	// uses for the project dir (runner.go:392). One path to think about; the
	// kubeconfig points at the same string a host-side `kubectl` would use.
	// Mount the FILE only, not the parent dir, so the admin kubeconfig sitting
	// next to it stays invisible to the cell.
	fmt.Fprintf(out, "Add to your .devcell.toml:\n\n")
	fmt.Fprintf(out, "    [[volumes]]\n")
	fmt.Fprintf(out, "    mount = \"%s:%s:ro\"\n\n", opts.Output, opts.Output)
	fmt.Fprintf(out, "    [env]\n")
	fmt.Fprintf(out, "    KUBECONFIG = \"%s\"\n\n", opts.Output)
	fmt.Fprintf(out, "Then restart the cell.\n")
}

// runKubectlEnv invokes kubectl with the given env and arguments, returning
// the combined stdout. The execCommand indirection makes it testable.
func runKubectlEnv(ctx context.Context, env []string, args ...string) (string, error) {
	cmd := execCommand(ctx, "kubectl", args...)
	cmd.Env = env
	var stderr strings.Builder
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		// Wrap with command + stderr for actionable error messages.
		return string(out), fmt.Errorf("kubectl %s: %s: %w",
			strings.Join(args, " "), strings.TrimSpace(stderr.String()), err)
	}
	return string(out), nil
}

// isAlreadyExists is true when kubectl create errored because the resource
// already exists — the idempotent re-run path.
func isAlreadyExists(err error) bool {
	if err == nil {
		return false
	}
	s := err.Error()
	return strings.Contains(s, "AlreadyExists") || strings.Contains(s, "already exists")
}
