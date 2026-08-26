package gosshd

import (
	"bufio"
	"fmt"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	cryptossh "golang.org/x/crypto/ssh"
)

// startTestServer serves s on a kernel-assigned loopback port and returns
// the address to dial.
func startTestServer(t *testing.T, s Server) string {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	t.Cleanup(func() { l.Close() })
	go s.Serve(l)
	return l.Addr().String()
}

func dialTestServer(t *testing.T, addr string) *cryptossh.Client {
	t.Helper()
	client, err := cryptossh.Dial("tcp", addr, &cryptossh.ClientConfig{
		User:            DefaultUser,
		Auth:            []cryptossh.AuthMethod{cryptossh.Password(DefaultPassword)},
		HostKeyCallback: cryptossh.InsecureIgnoreHostKey(),
		Timeout:         10 * time.Second,
	})
	require.NoError(t, err, "dialing test server")
	t.Cleanup(func() { client.Close() })
	return client
}

// There is no terminal behind a session — the child only ever gets pipes.
// Granting the PTY anyway is what made interactive typing land in a void:
// the client switches its terminal to raw mode (no local echo, Enter sends
// \r), while cmd.exe waits forever for a \n on its pipe. Refusing the
// request makes every ssh client fall back to cooked line mode, where
// typing echoes locally and lines arrive \n-terminated.
func TestServe_RefusesPtyRequests(t *testing.T) {
	addr := startTestServer(t, Server{User: DefaultUser, Password: DefaultPassword})
	client := dialTestServer(t, addr)

	sess, err := client.NewSession()
	require.NoError(t, err)
	defer sess.Close()

	err = sess.RequestPty("xterm-256color", 40, 80, cryptossh.TerminalModes{})
	assert.Error(t, err, "a PTY the server does not implement must be refused")
}

// The whole interactive path as an ssh client drives it: request a PTY (get
// refused), start a shell, type a line, see it execute. Runs against a POSIX
// shell so it works on the dev machine; the guest runs the same server code
// with cmd.exe.
func TestServe_InteractiveShellExecutesTypedLine(t *testing.T) {
	addr := startTestServer(t, Server{
		User:     DefaultUser,
		Password: DefaultPassword,
		command: func(request string) []string {
			if request == "" {
				return []string{"/bin/sh"}
			}
			return []string{"/bin/sh", "-c", request}
		},
	})
	client := dialTestServer(t, addr)

	sess, err := client.NewSession()
	require.NoError(t, err)
	defer sess.Close()

	assert.Error(t,
		sess.RequestPty("xterm-256color", 40, 80, cryptossh.TerminalModes{}),
		"interactive clients ask for a PTY first; it must be refused, not absorbed")

	stdin, err := sess.StdinPipe()
	require.NoError(t, err)
	stdout, err := sess.StdoutPipe()
	require.NoError(t, err)
	require.NoError(t, sess.Shell())

	lines := make(chan string)
	go func() {
		defer close(lines)
		scanner := bufio.NewScanner(stdout)
		for scanner.Scan() {
			lines <- scanner.Text()
		}
	}()

	fmt.Fprint(stdin, "echo INTERACTIVE_OK\n")

	deadline := time.After(10 * time.Second)
	for {
		select {
		case line, ok := <-lines:
			require.True(t, ok, "shell closed before echoing the typed line")
			if strings.Contains(line, "INTERACTIVE_OK") {
				fmt.Fprint(stdin, "exit\n")
				require.NoError(t, sess.Wait(), "shell exit after interactive session")
				return
			}
		case <-deadline:
			t.Fatal("typed line was never executed by the shell")
		}
	}
}
