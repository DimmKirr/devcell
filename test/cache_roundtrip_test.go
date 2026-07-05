package container_test

// cache_roundtrip_test — local proxy for the GHCR nix-cache pipeline
// in .github/workflows/build.dev.yml. Drives the EXACT same tar /
// crane / gunzip primitives against a localhost OCI registry so
// format/escaping/CLI-flag regressions surface in ~60 sec instead of
// ~30 min of CI.
//
// What this catches (the failure modes that bit us across CELL-292):
//   - `tar -czf` produces a layer that `crane append --new_layer -`
//     accepts without re-wrapping (one gunzip on pull, not two).
//   - `crane append --new_tag` arg syntax (string vs strings flag-type).
//   - `tar -C / nix` lands paths under `nix/...` so `--strip-components=1`
//     puts them under /dest/... on pull.
//   - File content survives the round-trip byte-for-byte.
//
// What this does NOT cover: runner disk exhaustion under the real
// ~32 GB uncompressed payload — that's size-dependent and only
// reproduces against a real runner-sized store.

import (
	"encoding/json"
	"fmt"
	"net"
	"os"
	osexec "os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func TestCacheRoundtrip(t *testing.T) {
	if testing.Short() {
		t.Skip("long: cache roundtrip exercises cell build + crane (~1+ min)")
	}

	ensureCrane(t)
	repoRoot := repoRoot(t)

	port := pickFreePort(t)
	regName := "cache-test-reg"
	srcVol := "devcell-cache-test-src"
	dstVol := "devcell-cache-test-dst"
	// hostAddr = how this test process reaches a Docker-published port.
	// On a bare host: 127.0.0.1 (the daemon publishes on 0.0.0.0, the
	// host's loopback is one valid binding). Inside a sibling-docker
	// container (e.g. devcell-158 running these tests via /var/run/docker.sock):
	// 127.0.0.1 is our own loopback, NOT the daemon's. The daemon's
	// published ports are reachable at our default gateway (the docker
	// bridge gateway). dockerHostAddr() picks the right one.
	hostAddr := dockerHostAddr(t)
	srcTag := fmt.Sprintf("%s:%d/cache-test:latest", hostAddr, port)
	baseTag := fmt.Sprintf("%s:%d/busybox:latest", hostAddr, port)

	// ── Setup: local OCI registry on a random unused port ────────────
	dockerRun(t, "rm", "-f", regName)
	dockerRun(t, "run", "-d", "--rm",
		"-p", fmt.Sprintf("%d:5000", port),
		"--name", regName,
		"public.ecr.aws/docker/library/registry:2")
	t.Cleanup(func() { dockerRun(t, "rm", "-f", regName) })
	waitForRegistry(t, hostAddr, port)

	// ── Setup: mirror busybox base into the local registry once ──────
	// Production publish uses `--base public.ecr.aws/docker/library/busybox`.
	// Mirroring it here lets `crane append` resolve `--base` against
	// localhost. Source preference: locally-cached docker image →
	// `crane copy` from public ECR. Local cache avoids the public-ECR
	// rate limit that bites on iterative test runs.
	mirrorBusybox(t, baseTag)

	// ── Setup: fixture — build the `core` stack into a fresh volume ──
	dockerRun(t, "volume", "rm", srcVol)
	dockerRun(t, "volume", "create", srcVol)
	t.Cleanup(func() { dockerRun(t, "volume", "rm", srcVol) })
	cellBuildCore(t, repoRoot, srcVol)
	logVolumeStats(t, srcVol, "src")

	// ── Push: tar -czf - | crane append --new_layer - ────────────────
	// Mirrors `.github/workflows/build.dev.yml` `Inspect + stream-publish`
	// step exactly, just against localhost.
	cellNixStorePush(t, repoRoot, srcVol, baseTag, srcTag)

	// ── Pull: crane blob | gunzip | tar -x ───────────────────────────
	// Mirrors the workflow's `Stream-hydrate /nix volume` step.
	dockerRun(t, "volume", "rm", dstVol)
	dockerRun(t, "volume", "create", dstVol)
	t.Cleanup(func() { dockerRun(t, "volume", "rm", dstVol) })
	cellNixStorePull(t, repoRoot, srcTag, dstVol)
	_ = hostAddr // diagnostic-only now (still used in pull's encoding probe via crane)
	_ = port
	logVolumeStats(t, dstVol, "dst")

	// ── Verify: byte-for-byte volume content match ───────────────────
	verifyVolumesMatch(t, srcVol, dstVol)
}

// ── helpers ─────────────────────────────────────────────────────────

func repoRoot(t *testing.T) string {
	t.Helper()
	_, thisFile, _, _ := runtime.Caller(0)
	return filepath.Dir(filepath.Dir(thisFile))
}

func pickFreePort(t *testing.T) int {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("pick free port: %v", err)
	}
	defer l.Close()
	return l.Addr().(*net.TCPAddr).Port
}

func dockerRun(t *testing.T, args ...string) string {
	t.Helper()
	out, err := osexec.Command("docker", args...).CombinedOutput()
	if err != nil {
		// Tolerate "rm -f" / "volume rm" on missing — Docker returns
		// non-zero when the target doesn't exist, but we always call
		// these defensively before create.
		if (args[0] == "rm" || (len(args) > 1 && args[0] == "volume" && args[1] == "rm")) &&
			(strings.Contains(string(out), "No such") || strings.Contains(string(out), "no such")) {
			return string(out)
		}
		t.Fatalf("docker %s failed: %v\n%s", strings.Join(args, " "), err, out)
	}
	return string(out)
}

func craneRun(t *testing.T, args ...string) string {
	t.Helper()
	out, err := osexec.Command("crane", args...).CombinedOutput()
	if err != nil {
		t.Fatalf("crane %s failed: %v\n%s", strings.Join(args, " "), err, out)
	}
	return string(out)
}

// mirrorBusybox publishes a busybox image to baseTag using whatever
// source is cheapest: first a locally-cached docker image (via
// `docker save | crane push`), then `crane copy` from public ECR
// as a fallback. The local-image path avoids public ECR's
// `TOOMANYREQUESTS` rate limit, which trips on iterative test runs.
func mirrorBusybox(t *testing.T, baseTag string) {
	t.Helper()
	candidates := []string{
		"busybox:latest",
		"busybox:1.36",
		"public.ecr.aws/docker/library/busybox:latest",
	}
	var local string
	for _, ref := range candidates {
		if err := osexec.Command("docker", "image", "inspect", ref).Run(); err == nil {
			local = ref
			break
		}
	}
	if local == "" {
		// No local copy — try crane copy from public (may rate-limit).
		t.Logf("no busybox cached locally; falling back to `crane copy` from public ECR")
		craneRun(t, "copy",
			"public.ecr.aws/docker/library/busybox:latest", baseTag)
		return
	}
	t.Logf("mirroring local %s → %s via docker save | crane push", local, baseTag)
	tmp, err := os.CreateTemp("", "busybox-*.tar")
	if err != nil {
		t.Fatalf("create temp tarball: %v", err)
	}
	tmpPath := tmp.Name()
	tmp.Close()
	t.Cleanup(func() { os.Remove(tmpPath) })
	if out, err := osexec.Command("docker", "save", "-o", tmpPath, local).CombinedOutput(); err != nil {
		t.Fatalf("docker save %s: %v\n%s", local, err, out)
	}
	craneRun(t, "push", tmpPath, baseTag)
}

func ensureCrane(t *testing.T) {
	t.Helper()
	if _, err := osexec.LookPath("crane"); err == nil {
		return
	}
	t.Fatalf("crane not on PATH — install with: " +
		"curl -fsSL https://github.com/google/go-containerregistry/releases/download/v0.20.6/go-containerregistry_Linux_$(uname -m | sed 's/x86_64/x86_64/;s/aarch64/arm64/').tar.gz | tar -xzC $HOME/.local/bin crane")
}

func waitForRegistry(t *testing.T, host string, port int) {
	t.Helper()
	deadline := time.Now().Add(15 * time.Second)
	addr := fmt.Sprintf("%s:%d", host, port)
	for time.Now().Before(deadline) {
		conn, err := net.DialTimeout("tcp", addr, 200*time.Millisecond)
		if err == nil {
			conn.Close()
			return
		}
		time.Sleep(200 * time.Millisecond)
	}
	t.Fatalf("registry at %s never became reachable", addr)
}

// dockerHostAddr returns the address the test process should use to reach
// Docker-published ports. On a bare host: 127.0.0.1. Inside a
// sibling-docker container (where 127.0.0.1 is our own loopback, not
// the daemon's): the default-route gateway, which IS the docker bridge
// gateway on a sibling-docker setup and the daemon publishes ports
// reachable there.
func dockerHostAddr(t *testing.T) string {
	t.Helper()
	if _, err := os.Stat("/.dockerenv"); err != nil {
		return "127.0.0.1"
	}
	out, err := osexec.Command("ip", "route", "show", "default").Output()
	if err != nil {
		t.Logf("ip route show default failed (%v); falling back to 127.0.0.1", err)
		return "127.0.0.1"
	}
	// Output looks like: "default via 172.18.0.1 dev eth0"
	fields := strings.Fields(string(out))
	for i, f := range fields {
		if f == "via" && i+1 < len(fields) {
			return fields[i+1]
		}
	}
	t.Logf("could not parse default gateway from %q; falling back to 127.0.0.1", string(out))
	return "127.0.0.1"
}

func cellBuildCore(t *testing.T, repoRoot, srcVol string) {
	t.Helper()
	cmd := osexec.Command(filepath.Join(repoRoot, "bin/cell"),
		"build", "--thin", "--stack", "core",
		"--image", "cache-test-cell:fixture")
	cmd.Env = append(os.Environ(), "DEVCELL_NIX_VOLUME="+srcVol)
	cmd.Dir = repoRoot
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("cell build --stack core failed: %v\n%s", err, out)
	}
}

// cellNixStorePush invokes the SAME `cell nix-store push` binary the
// CI workflow uses to publish the cache layer. By going through the
// cell subcommand (which uses `pkg/v1/stream.NewLayer` internally),
// the test exercises the identical code path as production — no
// file-vs-stdin or library-vs-CLI divergence is possible.
func cellNixStorePush(t *testing.T, repoRoot, srcVol, baseTag, srcTag string) {
	t.Helper()
	// Build the tar stream inside alpine (matches the workflow), then
	// pipe stdin → `./bin/cell nix-store push --base BASE --image DST`.
	tarCmd := osexec.Command("docker", "run", "--rm",
		"-v", srcVol+":/nix:ro",
		"public.ecr.aws/docker/library/alpine:latest",
		"tar", "-cf", "-",
		"--exclude=nix/var/nix/daemon-socket",
		"-C", "/", "nix")
	pushCmd := osexec.Command(filepath.Join(repoRoot, "bin/cell"),
		"nix-store", "push",
		"--base", baseTag,
		"--image", srcTag)
	pushCmd.Dir = repoRoot
	pipeR, pipeW := pipePair(t)
	tarCmd.Stdout = pipeW
	pushCmd.Stdin = pipeR
	var tarErr, pushOut, pushErr bytesBuffer
	tarCmd.Stderr = &tarErr
	pushCmd.Stdout = &pushOut
	pushCmd.Stderr = &pushErr

	if err := pushCmd.Start(); err != nil {
		t.Fatalf("start cell nix-store push: %v", err)
	}
	if err := tarCmd.Run(); err != nil {
		pipeW.Close()
		_ = pushCmd.Wait()
		t.Fatalf("tar -cf failed: %v\nstderr: %s", err, tarErr.String())
	}
	pipeW.Close()
	if err := pushCmd.Wait(); err != nil {
		t.Fatalf("cell nix-store push failed: %v\nstderr: %s\nstdout: %s", err, pushErr.String(), pushOut.String())
	}
	if pushOut.Len() > 0 {
		t.Logf("cell nix-store push stdout: %s", pushOut.String())
	}
}

// cellNixStorePull invokes `cell nix-store pull` to extract the cache
// image's last layer into a named Docker volume. Verifies the layer's
// byte-magic (single-gzipped on the wire) before the pull as a guard
// against any future regression in the push path.
func cellNixStorePull(t *testing.T, repoRoot, srcTag string, dstVol string) {
	t.Helper()

	// Encoding sanity check — verify the layer is single-gzipped, not
	// double. If `cell nix-store push` ever regresses to producing
	// double-gzipped layers, this surfaces immediately with a clear
	// diagnostic instead of cascading into "tar: invalid tar magic"
	// failures deeper down.
	manifest := craneRun(t, "manifest", srcTag)
	t.Logf("cache image manifest:\n%s", manifest)
	digest := lastLayerDigest(t, manifest)
	// srcTag is `host:port/repo:tag` — strip the trailing ":tag" to
	// build a blob ref. Use LastIndex, not IndexByte (which would cut
	// at the port colon).
	repo := srcTag
	if i := strings.LastIndex(repo, ":"); i >= 0 {
		repo = repo[:i]
	}
	blobRef := fmt.Sprintf("%s@%s", repo, digest)
	rawPeek := osShellOutput(t, fmt.Sprintf("crane blob %q | head -c 4 | xxd -p | head -1", blobRef))
	gunzipPeek := osShellOutput(t, fmt.Sprintf("crane blob %q | gunzip 2>/dev/null | head -c 4 | xxd -p | head -1", blobRef))
	t.Logf("raw blob first 4 bytes (hex): %s", strings.TrimSpace(rawPeek))
	t.Logf("after 1 gunzip, first 4 bytes (hex): %s", strings.TrimSpace(gunzipPeek))
	if !strings.HasPrefix(strings.TrimSpace(rawPeek), "1f8b") {
		t.Fatalf("layer blob does not start with gzip magic (got %q) — expected OCI tar+gzip", strings.TrimSpace(rawPeek))
	}
	if strings.HasPrefix(strings.TrimSpace(gunzipPeek), "1f8b") {
		t.Fatalf("after one gunzip the bytes are STILL gzip magic (got %q) — layer is double-gzipped. "+
			"`cell nix-store push` should produce a single-gzipped layer via stream.NewLayer.", strings.TrimSpace(gunzipPeek))
	}

	// Pull via the same binary the workflow uses.
	cmd := osexec.Command(filepath.Join(repoRoot, "bin/cell"),
		"nix-store", "pull",
		"--image", srcTag,
		"--volume", dstVol)
	cmd.Dir = repoRoot
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("cell nix-store pull failed: %v\noutput:\n%s", err, out)
	}
	if len(out) > 0 {
		t.Logf("cell nix-store pull output:\n%s", out)
	}
}

// pipePair returns an os.Pipe with a t.Cleanup ensuring it's closed.
func pipePair(t *testing.T) (*os.File, *os.File) {
	t.Helper()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("os.Pipe: %v", err)
	}
	t.Cleanup(func() { r.Close(); w.Close() })
	return r, w
}

// bytesBuffer is a tiny alias to avoid importing bytes everywhere.
type bytesBuffer = strings.Builder

// osShellOutput runs a shell command and returns its captured output.
// Diagnostic-only — does not fail the test on non-zero exit (the
// caller is exploring, not asserting).
func osShellOutput(t *testing.T, script string) string {
	t.Helper()
	out, _ := osexec.Command("sh", "-c", script).CombinedOutput()
	return string(out)
}

// runShellPipe executes a sh -c script and fails the test with the full
// merged stdout/stderr on non-zero exit. Used in place of manually
// wiring up os/exec pipes between N processes.
func runShellPipe(t *testing.T, label, script string) {
	t.Helper()
	out, err := osexec.Command("sh", "-c", script).CombinedOutput()
	if err != nil {
		t.Fatalf("%s failed: %v\nscript: %s\noutput:\n%s", label, err, script, out)
	}
	if len(out) > 0 {
		t.Logf("%s output:\n%s", label, out)
	}
}

// lastLayerDigest extracts .layers[-1].digest from a `crane manifest` JSON.
func lastLayerDigest(t *testing.T, manifest string) string {
	t.Helper()
	type layer struct {
		Digest string `json:"digest"`
	}
	type m struct {
		Layers []layer `json:"layers"`
	}
	var parsed m
	if err := json.Unmarshal([]byte(manifest), &parsed); err != nil {
		t.Fatalf("parse manifest: %v\n%s", err, manifest)
	}
	if len(parsed.Layers) == 0 {
		t.Fatalf("manifest has no layers:\n%s", manifest)
	}
	return parsed.Layers[len(parsed.Layers)-1].Digest
}

func logVolumeStats(t *testing.T, vol, label string) {
	t.Helper()
	out, _ := osexec.Command("docker", "run", "--rm",
		"-v", vol+":/nix:ro",
		"public.ecr.aws/docker/library/alpine:latest",
		"sh", "-c",
		"echo size=$(du -sh /nix | cut -f1); "+
			"echo files=$(find /nix -type f 2>/dev/null | wc -l); "+
			"echo symlinks=$(find /nix -type l 2>/dev/null | wc -l)").CombinedOutput()
	t.Logf("%s volume:\n%s", label, out)
}

// verifyVolumesMatch sha256-sums every file in both volumes and diffs them.
func verifyVolumesMatch(t *testing.T, srcVol, dstVol string) {
	t.Helper()
	out, err := osexec.Command("docker", "run", "--rm",
		"-v", srcVol+":/src:ro",
		"-v", dstVol+":/dst:ro",
		"public.ecr.aws/docker/library/alpine:latest",
		"sh", "-c",
		`cd /src && find . -type f -print0 2>/dev/null | sort -z | xargs -0 sha256sum > /tmp/src.sums;
         cd /dst && find . -type f -print0 2>/dev/null | sort -z | xargs -0 sha256sum > /tmp/dst.sums;
         if diff /tmp/src.sums /tmp/dst.sums >/tmp/diff 2>&1; then
           echo "VERIFY OK ($(wc -l </tmp/src.sums) files match)"
           exit 0
         fi
         echo "VERIFY MISMATCH (first 20 lines):"
         head -20 /tmp/diff
         exit 1`).CombinedOutput()
	t.Logf("verify:\n%s", out)
	if err != nil {
		t.Fatalf("volume content mismatch (exit=%v)", err)
	}
}

