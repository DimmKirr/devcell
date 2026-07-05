package op

import (
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"strings"
)

var execCommand = exec.Command

// field is the structure of a single field in `op item get --format json` output.
type field struct {
	Label string `json:"label"`
	Value string `json:"value"`
}

// item is the top-level JSON structure from `op item get --format json`.
type item struct {
	Fields []field `json:"fields"`
}

// ResolveItems calls `op item get` for each item name and returns a merged
// map of label→value for all fields that have both a label and a value.
// Items later in the slice win on key conflict.
//
// Resolution is optimistic: a failure on one item is recorded in the returned
// []error slice and the loop continues with the next item. The caller is
// expected to surface the per-item errors and apply whatever did resolve.
func ResolveItems(items []string) (map[string]string, []error) {
	env := make(map[string]string)
	var errs []error
	for _, name := range items {
		out, err := execCommand("op", "item", "get", name, "--format", "json", "--reveal", "--cache").Output()
		if err != nil {
			errs = append(errs, opError(name, err))
			continue
		}
		var it item
		if err := json.Unmarshal(out, &it); err != nil {
			errs = append(errs, fmt.Errorf("op item get %s: parse JSON: %w", name, err))
			continue
		}
		for _, f := range it.Fields {
			if f.Label != "" && f.Value != "" {
				env[f.Label] = f.Value
			}
		}
	}
	return env, errs
}

func opError(name string, err error) error {
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) && len(exitErr.Stderr) > 0 {
		stderr := strings.TrimSpace(string(exitErr.Stderr))
		return fmt.Errorf("op item get %s: %s (exit %d)", name, stderr, exitErr.ExitCode())
	}
	return fmt.Errorf("op item get %s: %w", name, err)
}
