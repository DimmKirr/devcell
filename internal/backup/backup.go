package backup

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"time"
)

const (
	claudeJSONName = ".claude.json"
	maxBackups     = 5
)

// Backup creates a timestamped backup of .claude.json in cellHome.
// It creates cellHome if absent, touches .claude.json if absent,
// copies it to a timestamped file, and keeps only the newest maxBackups.
func Backup(cellHome string, now time.Time) error {
	if err := os.MkdirAll(cellHome, 0755); err != nil {
		return fmt.Errorf("mkdir cellhome: %w", err)
	}

	claudeJSON := filepath.Join(cellHome, claudeJSONName)

	// Touch: create if absent, read existing content
	f, err := os.OpenFile(claudeJSON, os.O_CREATE|os.O_RDWR, 0644)
	if err != nil {
		return fmt.Errorf("open .claude.json: %w", err)
	}
	content, err := os.ReadFile(claudeJSON)
	f.Close()
	if err != nil {
		content = []byte{}
	}

	// Write timestamped backup
	ts := now.Format("2006-01-02T15-04-05")
	backupName := fmt.Sprintf(".claude.json-devcell-%s-backup", ts)
	backupPath := filepath.Join(cellHome, backupName)
	if err := os.WriteFile(backupPath, content, 0644); err != nil {
		return fmt.Errorf("write backup: %w", err)
	}

	// Prune: keep only newest maxBackups
	return pruneBackups(cellHome)
}

func pruneBackups(cellHome string) error {
	pattern := filepath.Join(cellHome, "*-devcell-*-backup")
	matches, err := filepath.Glob(pattern)
	if err != nil {
		return err
	}
	if len(matches) <= maxBackups {
		return nil
	}
	// Sort ascending by name (timestamp-ordered filenames sort correctly)
	sort.Strings(matches)
	// Remove oldest (everything beyond the newest maxBackups)
	toRemove := matches[:len(matches)-maxBackups]
	for _, p := range toRemove {
		if err := os.Remove(p); err != nil && !os.IsNotExist(err) {
			return fmt.Errorf("remove backup %s: %w", p, err)
		}
	}
	return nil
}
