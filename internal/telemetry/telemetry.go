package telemetry

import (
	"os"
	"runtime"

	"github.com/DimmKirr/devcell/internal/version"
	"github.com/posthog/posthog-go"
)

const defaultAPIKey = "phc_BbYjyXk7rB3dTD7qqTDzbo6zcS623Jz6LP9ktRCNMaFw"

var (
	client      posthog.Client
	anonymousID string

	// captureHook, when non-nil, receives every Capture before it's enqueued.
	// Used only in tests.
	captureHook func(posthog.Capture)
)

func Init(configDir string) {
	cfg := LoadConfig(configDir)
	if !IsAllowed(cfg) {
		return
	}
	initClient(cfg)
}

func initClient(cfg Config) {
	apiKey := os.Getenv("DEVCELL_POSTHOG_PROJECT_KEY")
	if apiKey == "" {
		apiKey = defaultAPIKey
	}
	c, err := posthog.NewWithConfig(apiKey, posthog.Config{
		Endpoint: "https://us.i.posthog.com",
	})
	if err != nil {
		return
	}
	client = c
	anonymousID = cfg.AnonymousID
}

func Close() {
	if client != nil {
		client.Close()
		client = nil
	}
}

func Track(event string, props map[string]any) {
	if client == nil {
		return
	}
	p := posthog.NewProperties()
	for k, v := range props {
		p.Set(k, v)
	}
	p.Set("os", runtime.GOOS)
	p.Set("arch", runtime.GOARCH)
	p.Set("version", version.Full())
	c := posthog.Capture{
		DistinctId: anonymousID,
		Event:      event,
		Properties: p,
	}
	if captureHook != nil {
		captureHook(c)
	}
	client.Enqueue(c)
}

func TrackCommandRun(command, engine, stack string, modules []string, thin bool) {
	Track("command_run", map[string]any{
		"command": command,
		"engine":  engine,
		"stack":   stack,
		"modules": modules,
		"thin":    thin,
	})
}

func TrackCommandFinish(command string, durationMs int64, clean bool) {
	Track("command_finish", map[string]any{
		"command":     command,
		"duration_ms": durationMs,
		"exit_clean":  clean,
	})
}
