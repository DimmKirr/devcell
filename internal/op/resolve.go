package op

import (
	"encoding/json"
	"fmt"
	"os/exec"
)

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
func ResolveItems(items []string) (map[string]string, error) {
	env := make(map[string]string)
	for _, name := range items {
		out, err := exec.Command("op", "item", "get", name, "--format", "json", "--reveal", "--cache").Output()
		if err != nil {
			return nil, fmt.Errorf("op item get %s: %w", name, err)
		}
		var it item
		if err := json.Unmarshal(out, &it); err != nil {
			return nil, fmt.Errorf("op item get %s: parse JSON: %w", name, err)
		}
		for _, f := range it.Fields {
			if f.Label != "" && f.Value != "" {
				env[f.Label] = f.Value
			}
		}
	}
	return env, nil
}
