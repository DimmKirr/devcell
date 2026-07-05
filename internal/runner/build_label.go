package runner

import "fmt"

// BuildLabel renders a progress label for an image build. When the stack was
// explicitly chosen by the user (TOML `stack = "..."` or `--stack`/env
// override), the qualifier is surfaced so they know what's about to run.
// Otherwise it's omitted because stacks are deprecated (CELL-6/337) and
// surfacing the resolved-default misleads — implies an opt-in that wasn't made.
func BuildLabel(prefix, stack string, explicit bool) string {
	if !explicit {
		return prefix
	}
	return fmt.Sprintf("%s (stack=%s)", prefix, stack)
}
