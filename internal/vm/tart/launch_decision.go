package tart

// LaunchAction is one VM-acquisition step.
type LaunchAction int

const (
	ActionUseLocal  LaunchAction = iota // disk image exists locally
	ActionBuild                        // build from IPSW (full init + provision)
	ActionDryRun                       // dry-run mode, no VM work
	ActionPullTart                     // pull pre-built image from Tart OCI registry
)

// LaunchInputs are the inputs to DecideLaunchActions.
type LaunchInputs struct {
	DryRun        bool   // --dry-run set
	ExplicitBuild bool   // --build set, force rebuild
	DiskExists    bool   // disk image exists at expected path
	TartRef       string // OCI image ref for Tart pull (e.g. ghcr.io/cirruslabs/macos-sequoia-base:latest)
}

// DecideLaunchActions returns the ordered fallback sequence.
//
//	DryRun                → [DryRun]
//	ExplicitBuild         → [Build]
//	DiskExists            → [UseLocal]
//	TartRef (cold start)  → [PullTart]
//	cold start            → [Build]
func DecideLaunchActions(in LaunchInputs) []LaunchAction {
	switch {
	case in.DryRun:
		return []LaunchAction{ActionDryRun}
	case in.ExplicitBuild:
		return []LaunchAction{ActionBuild}
	case in.DiskExists:
		return []LaunchAction{ActionUseLocal}
	case in.TartRef != "":
		return []LaunchAction{ActionPullTart}
	default:
		return []LaunchAction{ActionBuild}
	}
}
