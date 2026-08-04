package telemetry

import (
	"crypto/rand"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

const configFile = "telemetry.json"

type Config struct {
	Enabled     bool   `json:"enabled"`
	AnonymousID string `json:"anonymous_id"`
}

func LoadConfig(configDir string) Config {
	data, err := os.ReadFile(filepath.Join(configDir, configFile))
	if err != nil {
		return Config{}
	}
	var cfg Config
	if err := json.Unmarshal(data, &cfg); err != nil {
		return Config{}
	}
	return cfg
}

func SaveConfig(configDir string, cfg Config) error {
	if err := os.MkdirAll(configDir, 0755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return err
	}
	tmp := filepath.Join(configDir, configFile+".tmp")
	if err := os.WriteFile(tmp, data, 0644); err != nil {
		return err
	}
	return os.Rename(tmp, filepath.Join(configDir, configFile))
}

func Enable(configDir string) (Config, error) {
	cfg := LoadConfig(configDir)
	cfg.Enabled = true
	if cfg.AnonymousID == "" {
		id, err := generateUUID()
		if err != nil {
			return cfg, err
		}
		cfg.AnonymousID = id
	}
	return cfg, SaveConfig(configDir, cfg)
}

func Disable(configDir string) (Config, error) {
	cfg := LoadConfig(configDir)
	cfg.Enabled = false
	return cfg, SaveConfig(configDir, cfg)
}

func IsAllowed(cfg Config) bool {
	if os.Getenv("DO_NOT_TRACK") == "1" {
		return false
	}
	return cfg.Enabled
}

func generateUUID() (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	b[6] = (b[6] & 0x0f) | 0x40 // version 4
	b[8] = (b[8] & 0x3f) | 0x80 // variant 10
	return fmt.Sprintf("%08x-%04x-%04x-%04x-%012x",
		b[0:4], b[4:6], b[6:8], b[8:10], b[10:16]), nil
}
