package session

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"time"
)

type Record struct {
	Started time.Time  `json:"started"`
	Stopped *time.Time `json:"stopped"`
	Clean   *bool      `json:"clean"`
	Tool    string     `json:"tool"`
	Args    []string   `json:"args"`
}

const cellJSON = ".devcell/cell.json"

func Begin(projectDir, tool string, args []string) (*Record, error) {
	if args == nil {
		args = []string{}
	}
	rec := &Record{
		Started: time.Now().UTC(),
		Tool:    tool,
		Args:    args,
	}
	if err := writeRecord(projectDir, rec); err != nil {
		return rec, err
	}
	return rec, nil
}

func (r *Record) Finish(projectDir string, waitErr error) error {
	now := time.Now().UTC()
	r.Stopped = &now
	clean := waitErr == nil
	r.Clean = &clean
	return writeRecord(projectDir, r)
}

func Read(projectDir string) (*Record, error) {
	p := filepath.Join(projectDir, cellJSON)
	data, err := os.ReadFile(p)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, os.ErrNotExist
		}
		return nil, err
	}
	var rec Record
	if err := json.Unmarshal(data, &rec); err != nil {
		return nil, err
	}
	return &rec, nil
}

func writeRecord(projectDir string, rec *Record) error {
	dir := filepath.Join(projectDir, ".devcell")
	if err := os.MkdirAll(dir, 0755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(rec, "", "  ")
	if err != nil {
		return err
	}
	tmp := filepath.Join(dir, "cell.json.tmp")
	if err := os.WriteFile(tmp, data, 0644); err != nil {
		return err
	}
	return os.Rename(tmp, filepath.Join(dir, "cell.json"))
}
