package container_test

// tmux_test.go — smoke test: tmux and tmuxp must be available in the container.

import (
	"strings"
	"testing"
)

// TestTmuxBinaries — tmux and tmuxp must be on PATH for the session user.
func TestTmuxBinaries(t *testing.T) {
	c := startEnvContainer(t)
	for _, tool := range []struct{ bin, args string }{
		{"tmux", "-V"},
		{"tmuxp", "--version"},
	} {
		out, code := asUser(t, c, tool.bin+" "+tool.args)
		if code != 0 {
			t.Errorf("FAIL %s not available (exit %d): %s", tool.bin, code, out)
		} else {
			t.Logf("PASS %s: %s", tool.bin, strings.SplitN(out, "\n", 2)[0])
		}
	}
}
