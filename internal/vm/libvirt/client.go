package libvirt

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/url"
	"strings"
	"time"

	golibvirt "github.com/digitalocean/go-libvirt"
)

// defaultTCPPort is libvirtd's standard TCP listen port.
const defaultTCPPort = "16509"

// Typed connection errors, so callers (preflight, CELL-376) can map each
// failure to its own remediation instead of showing a raw dial error.
var (
	// ErrUnreachable: TCP dial failed — libvirtd is not listening (or the
	// host is wrong).
	ErrUnreachable = errors.New("libvirtd unreachable")
	// ErrHandshake: TCP connected but the libvirt RPC handshake failed —
	// wrong service on the port, or auth rejected.
	ErrHandshake = errors.New("libvirt handshake failed")
)

// ParseURI validates a libvirt connection URI and returns the TCP dial
// address. Only qemu+tcp:// is supported: the CLI runs inside a Linux cell
// and reaches the host's libvirtd over TCP (qemu+ssh:// is future work).
func ParseURI(uri string) (string, error) {
	u, err := url.Parse(uri)
	if err != nil || u.Scheme != "qemu+tcp" || u.Host == "" {
		return "", fmt.Errorf("unsupported libvirt URI %q: only qemu+tcp://host[:port]/session|/system is supported", uri)
	}
	if u.Port() != "" {
		return u.Host, nil
	}
	return net.JoinHostPort(u.Hostname(), defaultTCPPort), nil
}

// Client is a connection to a libvirtd daemon.
type Client struct {
	l    *golibvirt.Libvirt
	conn net.Conn
}

// Connect dials the daemon named by uri and completes the libvirt handshake.
// The context bounds both the dial and the handshake.
func Connect(ctx context.Context, uri string) (*Client, error) {
	addr, err := ParseURI(uri)
	if err != nil {
		return nil, err
	}

	var d net.Dialer
	conn, err := d.DialContext(ctx, "tcp", addr)
	if err != nil {
		return nil, fmt.Errorf("%w: dial %s: %v", ErrUnreachable, addr, err)
	}

	// ConnectToURI has no context parameter; bound the handshake with a
	// connection deadline derived from ctx.
	if deadline, ok := ctx.Deadline(); ok {
		_ = conn.SetDeadline(deadline)
	}

	l := golibvirt.New(conn)
	remote := golibvirt.QEMUSession
	if strings.HasSuffix(strings.TrimSuffix(uri, "/"), "/system") {
		remote = golibvirt.QEMUSystem
	}
	if err := l.ConnectToURI(remote); err != nil {
		conn.Close()
		return nil, fmt.Errorf("%w: %v", ErrHandshake, err)
	}
	_ = conn.SetDeadline(time.Time{})

	return &Client{l: l, conn: conn}, nil
}

// Close disconnects from the daemon and closes the socket.
//
// go-libvirt's Disconnect tears the socket down itself, so both it and our
// conn.Close can report "use of closed network connection" — benign teardown
// noise, not a failure (it broke the first field run, 2026-07-30).
func (c *Client) Close() error {
	err := ignoreErrClosed(c.l.Disconnect())
	if cerr := ignoreErrClosed(c.conn.Close()); err == nil {
		err = cerr
	}
	return err
}

// ignoreErrClosed drops net.ErrClosed (already-closed socket) and passes
// every other error through.
func ignoreErrClosed(err error) error {
	if err == nil || errors.Is(err, net.ErrClosed) {
		return nil
	}
	return err
}

// ListDomains returns the names of all domains, active and inactive.
func (c *Client) ListDomains() ([]string, error) {
	doms, _, err := c.l.ConnectListAllDomains(1, 0)
	if err != nil {
		return nil, fmt.Errorf("listing domains: %w", err)
	}
	names := make([]string, 0, len(doms))
	for _, d := range doms {
		names = append(names, d.Name)
	}
	return names, nil
}

// DefineDomain registers (or replaces) a persistent domain from XML and
// returns its name.
func (c *Client) DefineDomain(xml string) (string, error) {
	dom, err := c.l.DomainDefineXML(xml)
	if err != nil {
		return "", fmt.Errorf("defining domain: %w", err)
	}
	return dom.Name, nil
}

// StartDomain boots a defined domain by name.
func (c *Client) StartDomain(name string) error {
	dom, err := c.l.DomainLookupByName(name)
	if err != nil {
		return fmt.Errorf("looking up domain %q: %w", name, err)
	}
	if err := c.l.DomainCreate(dom); err != nil {
		return fmt.Errorf("starting domain %q: %w", name, err)
	}
	return nil
}

// ShutdownDomain requests a graceful guest shutdown (ACPI).
func (c *Client) ShutdownDomain(name string) error {
	dom, err := c.l.DomainLookupByName(name)
	if err != nil {
		return fmt.Errorf("looking up domain %q: %w", name, err)
	}
	if err := c.l.DomainShutdown(dom); err != nil {
		return fmt.Errorf("shutting down domain %q: %w", name, err)
	}
	return nil
}

// DestroyDomain force-stops a domain (hard power-off).
func (c *Client) DestroyDomain(name string) error {
	dom, err := c.l.DomainLookupByName(name)
	if err != nil {
		return fmt.Errorf("looking up domain %q: %w", name, err)
	}
	if err := c.l.DomainDestroy(dom); err != nil {
		return fmt.Errorf("destroying domain %q: %w", name, err)
	}
	return nil
}

// UndefineDomain removes a persistent domain definition.
func (c *Client) UndefineDomain(name string) error {
	dom, err := c.l.DomainLookupByName(name)
	if err != nil {
		return fmt.Errorf("looking up domain %q: %w", name, err)
	}
	if err := c.l.DomainUndefine(dom); err != nil {
		return fmt.Errorf("undefining domain %q: %w", name, err)
	}
	return nil
}

// DomainState returns the libvirt run state for a domain (values from
// virDomainState: 1=running, 5=shutoff, ...).
func (c *Client) DomainState(name string) (int32, error) {
	dom, err := c.l.DomainLookupByName(name)
	if err != nil {
		return 0, fmt.Errorf("looking up domain %q: %w", name, err)
	}
	state, _, err := c.l.DomainGetState(dom, 0)
	if err != nil {
		return 0, fmt.Errorf("querying state of %q: %w", name, err)
	}
	return state, nil
}
