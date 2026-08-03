package libvirt

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/DimmKirr/devcell/internal/vm/qemu"
)

// --- Engine lifecycle (CELL-377) ---
//
// Boot = translate paths → domain XML → define → start → wait for SSH on the
// forwarded port, reached at the libvirt URI's hostname (the forward lives on
// the same machine as libvirtd). Shutdown = graceful, escalate to destroy.

type fakeDomains struct {
	definedXML  string
	started     []string
	shutdown    []string
	destroyed   []string
	state       int32 // returned by DomainState
	stateErr    error
	afterShut   int32 // state after ShutdownDomain was called
	shutApplied bool
	closed      bool
}

func (f *fakeDomains) DefineDomain(xml string) (string, error) {
	f.definedXML = xml
	return "fake-domain", nil
}
func (f *fakeDomains) StartDomain(name string) error {
	f.started = append(f.started, name)
	return nil
}
func (f *fakeDomains) ShutdownDomain(name string) error {
	f.shutdown = append(f.shutdown, name)
	f.shutApplied = true
	return nil
}
func (f *fakeDomains) DestroyDomain(name string) error {
	f.destroyed = append(f.destroyed, name)
	return nil
}
func (f *fakeDomains) DomainState(name string) (int32, error) {
	if f.stateErr != nil {
		return 0, f.stateErr
	}
	if f.shutApplied {
		return f.afterShut, nil
	}
	return f.state, nil
}
func (f *fakeDomains) Close() error { f.closed = true; return nil }

func engineSpec() qemu.Spec {
	return qemu.Spec{
		VMName:       "devcell-cell1",
		CPUs:         2,
		MemoryGB:     4,
		DiskPath:     "/home/dmitry/.devcell/inst/disk.qcow2",
		FirmwarePath: "/home/dmitry/.devcell/fw/code.fd",
		SSHPort:      2222,
		SSHHost:      "127.0.0.1",
	}
}

func testEngine(f *fakeDomains) (*Engine, *[]string) {
	var sshWaits []string
	e := NewEngine("qemu+tcp://host.docker.internal/session", engineSpec(), testMap())
	e.ConnectFn = func(ctx context.Context, uri string) (DomainClient, error) { return f, nil }
	e.WaitSSHFn = func(host string, port uint16, timeout time.Duration) error {
		sshWaits = append(sshWaits, host)
		return nil
	}
	return e, &sshWaits
}

func TestEngine_SSHHostDerivedFromURI(t *testing.T) {
	e := NewEngine("qemu+tcp://host.docker.internal/session", engineSpec(), nil)
	if got := e.SSHHost(); got != "host.docker.internal" {
		t.Errorf("SSHHost() = %q, want host.docker.internal", got)
	}
}

func TestEngine_BootDefinesTranslatedXMLThenStartsThenWaits(t *testing.T) {
	f := &fakeDomains{state: DomainShutoff}
	e, sshWaits := testEngine(f)

	if err := e.Boot(context.Background()); err != nil {
		t.Fatal(err)
	}
	if f.definedXML == "" {
		t.Fatal("Boot must define the domain")
	}
	if !strings.Contains(f.definedXML, "/Users/dmitry/.devcell/inst/disk.qcow2") {
		t.Errorf("defined XML must carry HOST paths, got:\n%s", f.definedXML)
	}
	if strings.Contains(f.definedXML, "/home/dmitry/.devcell") {
		t.Errorf("defined XML must not leak container paths, got:\n%s", f.definedXML)
	}
	if len(f.started) != 1 {
		t.Errorf("Boot must start the defined domain once, got %v", f.started)
	}
	if len(*sshWaits) != 1 || (*sshWaits)[0] != "host.docker.internal" {
		t.Errorf("Boot must wait for SSH at the URI host, got %v", *sshWaits)
	}
}

func TestEngine_BootBindsHostfwdOnAllInterfaces(t *testing.T) {
	// The container reaches the forward via host.docker.internal; a forward
	// bound to the mac's 127.0.0.1 is unreachable from the Docker VM.
	f := &fakeDomains{state: DomainShutoff}
	e, _ := testEngine(f)
	if err := e.Boot(context.Background()); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(f.definedXML, "hostfwd=tcp::2222-:22") {
		t.Errorf("hostfwd must bind all interfaces (empty host), got:\n%s", f.definedXML)
	}
}

func TestEngine_BootAttachesWhenAlreadyRunning(t *testing.T) {
	f := &fakeDomains{state: DomainRunning}
	e, sshWaits := testEngine(f)
	if err := e.Boot(context.Background()); err != nil {
		t.Fatal(err)
	}
	if f.definedXML != "" || len(f.started) != 0 {
		t.Errorf("running domain must be attached, not redefined/restarted (defined=%q started=%v)", f.definedXML, f.started)
	}
	if len(*sshWaits) != 1 {
		t.Errorf("attach must still verify SSH, got %v", *sshWaits)
	}
}

func TestEngine_BootPropagatesConnectFailure(t *testing.T) {
	e := NewEngine("qemu+tcp://host.docker.internal/session", engineSpec(), testMap())
	e.ConnectFn = func(ctx context.Context, uri string) (DomainClient, error) {
		return nil, ErrUnreachable
	}
	if err := e.Boot(context.Background()); !errors.Is(err, ErrUnreachable) {
		t.Errorf("Boot must propagate connect failure, got %v", err)
	}
}

func TestEngine_ShutdownGraceful(t *testing.T) {
	f := &fakeDomains{state: DomainRunning, afterShut: DomainShutoff}
	e, _ := testEngine(f)
	if err := e.Boot(context.Background()); err != nil {
		t.Fatal(err)
	}
	e.ShutdownPollInterval = time.Millisecond
	if err := e.Shutdown(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(f.shutdown) != 1 {
		t.Errorf("expected one graceful shutdown request, got %v", f.shutdown)
	}
	if len(f.destroyed) != 0 {
		t.Errorf("graceful path must not destroy, got %v", f.destroyed)
	}
}

func TestEngine_ShutdownEscalatesToDestroy(t *testing.T) {
	f := &fakeDomains{state: DomainRunning, afterShut: DomainRunning} // never shuts down
	e, _ := testEngine(f)
	if err := e.Boot(context.Background()); err != nil {
		t.Fatal(err)
	}
	e.ShutdownPollInterval = time.Millisecond
	e.ShutdownGraceTimeout = 5 * time.Millisecond
	if err := e.Shutdown(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(f.destroyed) != 1 {
		t.Errorf("stuck guest must be destroyed, got %v", f.destroyed)
	}
}

func TestEngine_ShutdownWithoutBootIsNoop(t *testing.T) {
	f := &fakeDomains{}
	e, _ := testEngine(f)
	if err := e.Shutdown(context.Background()); err != nil {
		t.Errorf("Shutdown before Boot must be a no-op, got %v", err)
	}
	if len(f.shutdown)+len(f.destroyed) != 0 {
		t.Error("no domain operations expected before Boot")
	}
}
