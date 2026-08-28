package libvirt

import (
	"context"
	"fmt"
	"net/url"
	"time"

	"github.com/DimmKirr/devcell/internal/vm/qemu"
)

// virDomainState values (subset).
const (
	DomainRunning int32 = 1
	DomainShutoff int32 = 5
)

// DomainClient is the slice of Client the engine needs; injectable in tests.
type DomainClient interface {
	DefineDomain(xml string) (string, error)
	StartDomain(name string) error
	ShutdownDomain(name string) error
	DestroyDomain(name string) error
	DomainState(name string) (int32, error)
	Close() error
}

// Engine boots and stops a prepped Windows template on the machine behind a
// libvirtd connection (CELL-377). It implements the vm.Engine lifecycle.
type Engine struct {
	URI  string
	Spec qemu.Spec
	Map  PathMap

	// SSHWaitTimeout bounds the post-boot SSH wait. The template is already
	// installed and provisioned, so this is a boot, not a Windows install.
	SSHWaitTimeout time.Duration
	// ShutdownGraceTimeout bounds the graceful-shutdown wait before escalating
	// to destroy.
	ShutdownGraceTimeout time.Duration
	// ShutdownPollInterval is how often Shutdown re-checks the domain state.
	ShutdownPollInterval time.Duration

	// ConnectFn is the transport factory; tests inject a fake.
	ConnectFn func(ctx context.Context, uri string) (DomainClient, error)
	// WaitSSHFn waits for the forwarded SSH port; tests inject a fake.
	WaitSSHFn func(host string, port uint16, timeout time.Duration) error

	client DomainClient
	booted bool
}

// NewEngine builds an engine with production defaults.
func NewEngine(uri string, spec qemu.Spec, m PathMap) *Engine {
	e := &Engine{
		URI:                  uri,
		Spec:                 spec,
		Map:                  m,
		SSHWaitTimeout:       5 * time.Minute,
		ShutdownGraceTimeout: 30 * time.Second,
		ShutdownPollInterval: time.Second,
	}
	e.ConnectFn = func(ctx context.Context, u string) (DomainClient, error) {
		return Connect(ctx, u)
	}
	e.WaitSSHFn = func(host string, port uint16, timeout time.Duration) error {
		return qemu.WaitForSSH(host, port, timeout, 3*time.Second, qemu.NopObserver{})
	}
	return e
}

// SSHHost returns where the forwarded guest ports are reachable from the
// CLI's network namespace: the libvirt URI's hostname — the forward lives on
// the same machine as libvirtd.
func (e *Engine) SSHHost() string {
	if u, err := url.Parse(e.URI); err == nil && u.Hostname() != "" {
		return u.Hostname()
	}
	return "host.docker.internal"
}

// Preflight verifies the daemon answers (vm.Engine interface).
func (e *Engine) Preflight() error {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	return Preflight(ctx, e.URI)
}

// Boot defines and starts the domain, then waits for SSH. A domain that is
// already running is attached to instead of redefined.
func (e *Engine) Boot(ctx context.Context) error {
	client, err := e.ConnectFn(ctx, e.URI)
	if err != nil {
		return err
	}
	e.client = client

	state, stateErr := client.DomainState(e.Spec.VMName)
	if stateErr == nil && state == DomainRunning {
		// Attach: the VM is up; just verify SSH answers.
		e.booted = true
		return e.WaitSSHFn(e.SSHHost(), e.Spec.SSHPort, e.SSHWaitTimeout)
	}

	xml, err := e.DomainXML()
	if err != nil {
		return err
	}
	if _, err := client.DefineDomain(string(xml)); err != nil {
		return err
	}
	if err := client.StartDomain(e.Spec.VMName); err != nil {
		return err
	}
	e.booted = true
	return e.WaitSSHFn(e.SSHHost(), e.Spec.SSHPort, e.SSHWaitTimeout)
}

// DomainXML renders the domain document this engine would define: paths
// translated to the host namespace, hostfwd bound on all interfaces so the
// forward is reachable from the container (the mac's 127.0.0.1 is not).
func (e *Engine) DomainXML() ([]byte, error) {
	spec, err := TranslateSpecPaths(e.Spec, e.Map)
	if err != nil {
		return nil, err
	}
	spec.SSHHost = "" // hostfwd=tcp::PORT-:22 — bind all interfaces
	return SpecToDomainXML(spec)
}

// Shutdown requests a graceful stop and escalates to destroy when the guest
// does not power off within ShutdownGraceTimeout. A no-op before Boot.
func (e *Engine) Shutdown(ctx context.Context) error {
	if !e.booted || e.client == nil {
		return nil
	}
	defer func() {
		e.client.Close()
		e.client = nil
		e.booted = false
	}()

	if err := e.client.ShutdownDomain(e.Spec.VMName); err != nil {
		return fmt.Errorf("requesting shutdown: %w", err)
	}
	deadline := time.Now().Add(e.ShutdownGraceTimeout)
	for time.Now().Before(deadline) {
		state, err := e.client.DomainState(e.Spec.VMName)
		if err == nil && state == DomainShutoff {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(e.ShutdownPollInterval):
		}
	}
	return e.client.DestroyDomain(e.Spec.VMName)
}

// SSHArgv builds the exec argv for the booted guest (vm.Engine interface).
func (e *Engine) SSHArgv(binary string, flags, args []string) []string {
	spec := e.Spec
	spec.SSHHost = e.SSHHost()
	spec.Binary = binary
	spec.DefaultFlags = flags
	spec.UserArgs = args
	return qemu.BuildSSHArgv(spec)
}
