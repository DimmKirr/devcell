package tart

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"os/exec"
	"time"

	"github.com/DimmKirr/devcell/internal/ux"
	"golang.org/x/crypto/ssh"
)

// RunSSHCommand executes a command on the VM over SSH using key-based auth.
func RunSSHCommand(host string, port uint16, user, keyPath, command string, stdout, stderr io.Writer) error {
	keyBytes, err := os.ReadFile(keyPath)
	if err != nil {
		return fmt.Errorf("reading SSH key %s: %w", keyPath, err)
	}
	signer, err := ssh.ParsePrivateKey(keyBytes)
	if err != nil {
		return fmt.Errorf("parsing SSH key: %w", err)
	}
	config := &ssh.ClientConfig{
		User:            user,
		Auth:            []ssh.AuthMethod{ssh.PublicKeys(signer)},
		HostKeyCallback: ssh.InsecureIgnoreHostKey(),
		Timeout:         10 * time.Second,
	}
	return runSSH(host, port, config, keyPath, command, stdout, stderr)
}

// RunSSHCommandPassword executes a command on the VM over SSH using password auth.
// Tries both keyboard-interactive (macOS default via PAM) and plain password.
func RunSSHCommandPassword(host string, port uint16, user, password, command string, stdout, stderr io.Writer) error {
	kbInteractive := ssh.KeyboardInteractive(
		func(user, instruction string, questions []string, echos []bool) ([]string, error) {
			answers := make([]string, len(questions))
			for i := range questions {
				answers[i] = password
			}
			return answers, nil
		},
	)
	config := &ssh.ClientConfig{
		User:            user,
		Auth:            []ssh.AuthMethod{kbInteractive, ssh.Password(password)},
		HostKeyCallback: ssh.InsecureIgnoreHostKey(),
		Timeout:         10 * time.Second,
	}
	return runSSH(host, port, config, "", command, stdout, stderr)
}

func runSSH(host string, port uint16, config *ssh.ClientConfig, keyPath, command string, stdout, stderr io.Writer) error {
	addr := fmt.Sprintf("%s:%d", host, port)
	ux.Debugf("ssh: dialing %s as %s (auth methods: %d, timeout: %s, key: %s)",
		addr, config.User, len(config.Auth), config.Timeout, keyPath)

	client, err := ssh.Dial("tcp", addr, config)
	if err != nil {
		ux.Debugf("ssh: dial failed: %v", err)

		// Diagnostic: shell out to real ssh -vvv with the same key for protocol details.
		diagArgs := []string{"-vvv",
			"-o", "StrictHostKeyChecking=no",
			"-o", "UserKnownHostsFile=/dev/null",
			"-o", "BatchMode=yes",
			"-o", "ConnectTimeout=5",
			"-p", fmt.Sprintf("%d", port),
		}
		if keyPath != "" {
			diagArgs = append(diagArgs, "-i", keyPath, "-o", "IdentitiesOnly=yes")
		}
		diagArgs = append(diagArgs, fmt.Sprintf("%s@%s", config.User, host), "true")
		sshDiag := exec.Command("ssh", diagArgs...)
		diagOut, _ := sshDiag.CombinedOutput()
		ux.Debugf("ssh: diagnostic ssh -vvv output:\n%s", diagOut)
		return fmt.Errorf("SSH dial %s: %w", addr, err)
	}
	defer client.Close()
	ux.Debugf("ssh: connected to %s (server version: %s)", addr, string(client.ServerVersion()))

	session, err := client.NewSession()
	if err != nil {
		ux.Debugf("ssh: session creation failed: %v", err)
		return fmt.Errorf("SSH session: %w", err)
	}
	defer session.Close()

	session.Stdout = stdout
	session.Stderr = stderr
	ux.Debugf("ssh: running command: %s", command)
	if err := session.Run(command); err != nil {
		var stderrBuf bytes.Buffer
		if stderr == nil {
			session.Stderr = &stderrBuf
		}
		ux.Debugf("ssh: command failed: %v", err)
		return fmt.Errorf("SSH command failed: %w", err)
	}
	ux.Debugf("ssh: command completed successfully")
	return nil
}
