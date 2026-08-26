// Command gosshd is the SSH server staged into a WinPE guest.
//
// Usage: gosshd.exe [logfile]
//
// It is cross-compiled for windows/arm64 by the host and copied onto the
// agent volume; see internal/vm/qemu. The log path is a positional argument
// because the guest has no environment to configure it through: pointing it
// at the shared volume is what keeps the log readable after the VM is gone.
package main

import (
	"io"
	"log"
	"os"

	"github.com/DimmKirr/devcell/internal/gosshd"
)

func main() {
	out := io.Writer(os.Stderr)
	if len(os.Args) > 1 {
		f, err := os.OpenFile(os.Args[1], os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
		if err != nil {
			log.Printf("cannot open log %s: %v", os.Args[1], err)
		} else {
			defer f.Close()
			out = io.MultiWriter(os.Stderr, f)
		}
	}
	logger := log.New(out, "gosshd: ", log.LstdFlags)

	srv := gosshd.Server{
		Addr:     gosshd.DefaultAddr,
		User:     gosshd.DefaultUser,
		Password: gosshd.DefaultPassword,
		Log:      logger,
	}
	logger.Fatal(srv.ListenAndServe())
}
