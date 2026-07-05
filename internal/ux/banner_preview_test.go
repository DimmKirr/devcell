package ux_test

import (
	"fmt"
	"os"
	"testing"

	"github.com/DimmKirr/devcell/internal/ux"
)

// TestPreviewBanner is a manual visual check — run with `go test -v -run
// TestPreviewBanner ./internal/ux/` to eyeball the rendered output. Always
// passes; exists for `go test` invocation parity (no manual binary build).
func TestPreviewBanner(t *testing.T) {
	if os.Getenv("DEVCELL_PREVIEW") == "" {
		t.Skip("set DEVCELL_PREVIEW=1 to render the banner preview")
	}
	fmt.Println()
	fmt.Println(" " + ux.Banner("DIMM", "devcell", "304"))
	fmt.Println()
	const w = 8
	fmt.Println("   " + ux.KV(w, "Project", "devcell"+ux.StyleMuted.Render("  /Users/dmitry/dev/dimmkirr/devcell")))
	fmt.Println("   " + ux.KV(w, "Cell", "DIMM"+ux.StyleMuted.Render("  /Users/dmitry/.devcell/DIMM")))
	fmt.Println("   " + ux.KV(w, "Image", "devcell-user:ultimate-thin"+ux.StyleMuted.Render("  1.1 GB")))
	fmt.Println("   " + ux.KV(w, "Modules", "stack=ultimate"))
	fmt.Println("   " + ux.KV(w, "Network", "devcell-network"+ux.StyleMuted.Render(" · hostname devcell-304 · MAC auto")))
	fmt.Println("   " + ux.KV(w, "Locale", "en_US.UTF-8 (from host $LANG)"))
	fmt.Println("   " + ux.KV(w, "Timezone", "UTC (from host $TZ)"))
	fmt.Println("   " + ux.KV(w, "Ports", "VNC localhost:30450"+ux.StyleMuted.Render(" · ")+"RDP localhost:30489"))
	fmt.Println()
}
