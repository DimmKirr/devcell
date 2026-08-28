package cfg_test

import (
	"path/filepath"
	"testing"

	"github.com/DimmKirr/devcell/internal/cfg"
)

// --- libvirt_uri field (CELL-372) ---
//
// `[cell] libvirt_uri` points the libvirt remote-run mode at a libvirtd
// daemon. The default targets the macOS host's session daemon as seen from
// inside a Docker cell: qemu+tcp://host.docker.internal/session.

func TestLoadFile_LibvirtURI(t *testing.T) {
	dir := t.TempDir()
	writeTOML(t, dir, "devcell.toml", `
[cell]
libvirt_uri = "qemu+ssh://user@mac/session"
`)
	c, err := cfg.LoadFile(filepath.Join(dir, "devcell.toml"))
	if err != nil {
		t.Fatal(err)
	}
	if got := c.Cell.LibvirtURI; got != "qemu+ssh://user@mac/session" {
		t.Errorf("LibvirtURI = %q, want %q", got, "qemu+ssh://user@mac/session")
	}
}

func TestResolvedLibvirtURI_Default(t *testing.T) {
	c := cfg.CellSection{}
	if got := c.ResolvedLibvirtURI(); got != cfg.DefaultLibvirtURI {
		t.Errorf("ResolvedLibvirtURI() = %q, want default %q", got, cfg.DefaultLibvirtURI)
	}
}

func TestDefaultLibvirtURI_TargetsDockerHostSession(t *testing.T) {
	if cfg.DefaultLibvirtURI != "qemu+tcp://host.docker.internal/session" {
		t.Errorf("DefaultLibvirtURI = %q, want qemu+tcp://host.docker.internal/session", cfg.DefaultLibvirtURI)
	}
}

func TestResolvedLibvirtURI_TOMLOverridesDefault(t *testing.T) {
	c := cfg.CellSection{LibvirtURI: "qemu+tcp://10.0.0.5/system"}
	if got := c.ResolvedLibvirtURI(); got != "qemu+tcp://10.0.0.5/system" {
		t.Errorf("ResolvedLibvirtURI() = %q, want toml value", got)
	}
}

func TestResolvedLibvirtURI_EnvOverridesTOML(t *testing.T) {
	t.Setenv("DEVCELL_LIBVIRT_URI", "qemu+tcp://envhost/session")
	c := cfg.CellSection{LibvirtURI: "qemu+tcp://tomlhost/session"}
	if got := c.ResolvedLibvirtURI(); got != "qemu+tcp://envhost/session" {
		t.Errorf("ResolvedLibvirtURI() = %q, want env value", got)
	}
}

func TestMerge_LibvirtURIProjectWins(t *testing.T) {
	global := cfg.CellConfig{Cell: cfg.CellSection{LibvirtURI: "qemu+tcp://global/session"}}
	project := cfg.CellConfig{Cell: cfg.CellSection{LibvirtURI: "qemu+tcp://project/session"}}
	if got := cfg.Merge(global, project).Cell.LibvirtURI; got != "qemu+tcp://project/session" {
		t.Errorf("merged LibvirtURI = %q, want project value", got)
	}
}

func TestMerge_LibvirtURIGlobalKeptWhenProjectUnset(t *testing.T) {
	global := cfg.CellConfig{Cell: cfg.CellSection{LibvirtURI: "qemu+tcp://global/session"}}
	if got := cfg.Merge(global, cfg.CellConfig{}).Cell.LibvirtURI; got != "qemu+tcp://global/session" {
		t.Errorf("merged LibvirtURI = %q, want global value preserved", got)
	}
}

// Engine must survive Merge: a project-level `engine = "libvirt"` is how the
// dispatch in runAgent/build/init selects the engine, and Merge starts from
// the global Cell section — without an explicit override the project value
// silently vanishes whenever a global config file exists.
func TestMerge_EngineProjectWins(t *testing.T) {
	global := cfg.CellConfig{Cell: cfg.CellSection{Engine: "docker"}}
	project := cfg.CellConfig{Cell: cfg.CellSection{Engine: "libvirt"}}
	if got := cfg.Merge(global, project).Cell.Engine; got != "libvirt" {
		t.Errorf("merged Engine = %q, want %q", got, "libvirt")
	}
}

// --- libvirt_path_map (CELL-375) ---

func TestLoadFile_LibvirtPathMap(t *testing.T) {
	dir := t.TempDir()
	writeTOML(t, dir, "devcell.toml", `
[cell.libvirt_path_map]
"/devcell-155" = "/Users/dmitry/dev/dimmkirr/devcell"
"/home/dmitry" = "/Users/dmitry"
`)
	c, err := cfg.LoadFile(filepath.Join(dir, "devcell.toml"))
	if err != nil {
		t.Fatal(err)
	}
	if got := c.Cell.LibvirtPathMap["/devcell-155"]; got != "/Users/dmitry/dev/dimmkirr/devcell" {
		t.Errorf("LibvirtPathMap[/devcell-155] = %q", got)
	}
	if got := c.Cell.LibvirtPathMap["/home/dmitry"]; got != "/Users/dmitry" {
		t.Errorf("LibvirtPathMap[/home/dmitry] = %q", got)
	}
}

func TestMerge_LibvirtPathMapAccumulates(t *testing.T) {
	global := cfg.CellConfig{Cell: cfg.CellSection{LibvirtPathMap: map[string]string{
		"/home/dmitry": "/Users/dmitry",
	}}}
	project := cfg.CellConfig{Cell: cfg.CellSection{LibvirtPathMap: map[string]string{
		"/devcell-155": "/Users/dmitry/dev/dimmkirr/devcell",
	}}}
	m := cfg.Merge(global, project).Cell.LibvirtPathMap
	if m["/home/dmitry"] != "/Users/dmitry" || m["/devcell-155"] != "/Users/dmitry/dev/dimmkirr/devcell" {
		t.Errorf("merged map must accumulate both entries, got %v", m)
	}
}

func TestMerge_LibvirtPathMapProjectOverridesSameKey(t *testing.T) {
	global := cfg.CellConfig{Cell: cfg.CellSection{LibvirtPathMap: map[string]string{
		"/home/dmitry": "/wrong",
	}}}
	project := cfg.CellConfig{Cell: cfg.CellSection{LibvirtPathMap: map[string]string{
		"/home/dmitry": "/Users/dmitry",
	}}}
	if got := cfg.Merge(global, project).Cell.LibvirtPathMap["/home/dmitry"]; got != "/Users/dmitry" {
		t.Errorf("project entry must win for same key, got %q", got)
	}
}

// --- qemu_project_sync (CELL-383) ---
//
// Controls the scp project sync used by the qemu and libvirt engines:
// "push" (default: copy project into the guest before exec), "two-way"
// (also pull it back on exit), "off". Invalid values resolve to "push".

func TestResolvedQemuProjectSync_DefaultPush(t *testing.T) {
	c := cfg.CellSection{}
	if got := c.ResolvedQemuProjectSync(); got != "push" {
		t.Errorf("default = %q, want push", got)
	}
}

func TestResolvedQemuProjectSync_TOML(t *testing.T) {
	c := cfg.CellSection{QemuProjectSync: "two-way"}
	if got := c.ResolvedQemuProjectSync(); got != "two-way" {
		t.Errorf("got %q, want two-way", got)
	}
}

func TestResolvedQemuProjectSync_EnvWins(t *testing.T) {
	t.Setenv("DEVCELL_QEMU_PROJECT_SYNC", "off")
	c := cfg.CellSection{QemuProjectSync: "two-way"}
	if got := c.ResolvedQemuProjectSync(); got != "off" {
		t.Errorf("got %q, want env value off", got)
	}
}

func TestResolvedQemuProjectSync_InvalidFallsBackToPush(t *testing.T) {
	c := cfg.CellSection{QemuProjectSync: "sideways"}
	if got := c.ResolvedQemuProjectSync(); got != "push" {
		t.Errorf("invalid value must resolve to push, got %q", got)
	}
}

func TestLoadFile_QemuProjectSync(t *testing.T) {
	dir := t.TempDir()
	writeTOML(t, dir, "devcell.toml", `
[cell]
qemu_project_sync = "off"
`)
	c, err := cfg.LoadFile(filepath.Join(dir, "devcell.toml"))
	if err != nil {
		t.Fatal(err)
	}
	if c.Cell.QemuProjectSync != "off" {
		t.Errorf("QemuProjectSync = %q, want off", c.Cell.QemuProjectSync)
	}
}

func TestMerge_EngineGlobalKeptWhenProjectUnset(t *testing.T) {
	global := cfg.CellConfig{Cell: cfg.CellSection{Engine: "qemu"}}
	if got := cfg.Merge(global, cfg.CellConfig{}).Cell.Engine; got != "qemu" {
		t.Errorf("merged Engine = %q, want global value preserved", got)
	}
}
