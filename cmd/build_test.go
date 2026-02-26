package main_test

import (
	"os"
	"os/exec"
	"testing"
)

func TestVet(t *testing.T) {
	cmd := exec.Command("go", "vet", "./...")
	cmd.Env = append(os.Environ(), "GOMODCACHE=/tmp/gomodcache", "GOPATH=/tmp/gopath")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("go vet failed:\n%s", out)
	}
}

func TestBuildBinarySize(t *testing.T) {
	tmp, err := os.MkdirTemp("", "cell-dist-*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tmp)

	binPath := tmp + "/cell"
	cmd := exec.Command("go", "build", "-o", binPath, ".")
	cmd.Env = append(os.Environ(), "CGO_ENABLED=0", "GOMODCACHE=/tmp/gomodcache", "GOPATH=/tmp/gopath")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("go build failed:\n%s", out)
	}

	info, err := os.Stat(binPath)
	if err != nil {
		t.Fatal(err)
	}
	const maxSize = 20 * 1024 * 1024 // 20 MB
	if info.Size() > maxSize {
		t.Errorf("binary size %d exceeds 20MB limit", info.Size())
	}
}
