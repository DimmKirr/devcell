package qemu

// LaunchAction is one VM-acquisition step.
type LaunchAction int

const (
	ActionUseLocal LaunchAction = iota // instance disk exists locally
	ActionBuild                        // build from Windows ISO (full install + provision)
	ActionDryRun                       // dry-run mode, no VM work
	ActionAttach                       // attach to already-running VM
	ActionClone                        // clone existing template to instance
)

// MinTemplateSizeBytes is the minimum valid template disk size (4 GB).
// A template smaller than this is corrupt or incomplete (e.g. a failed build
// left a 193 KB stub) and should trigger a rebuild instead of a clone.
const MinTemplateSizeBytes int64 = 4 * 1024 * 1024 * 1024

// LaunchInputs are the inputs to DecideLaunchActions.
type LaunchInputs struct {
	DryRun            bool  // --dry-run set
	ExplicitBuild     bool  // --force set, force rebuild
	DiskExists        bool  // instance disk image exists at expected path
	DiskSizeBytes     int64 // instance disk file size; 0 means unchecked
	TemplateExists    bool  // template disk image exists (ready to clone)
	TemplateSizeBytes int64 // template disk file size; 0 means unchecked
	VMRunning         bool  // existing QEMU process detected via PID file + QMP
	Provisioned       bool  // .provisioned marker exists
}

func diskValid(size int64) bool {
	return size == 0 || size >= MinTemplateSizeBytes
}

// DecideLaunchActions returns the ordered fallback sequence.
//
//	DryRun                        → [DryRun]
//	ExplicitBuild                 → [Build]
//	VMRunning                     → [Attach]
//	DiskExists + valid + prov'd   → [UseLocal]
//	DiskExists + invalid/unprov'd → [Build]  (corrupt leftovers)
//	TemplateExists + valid        → [Clone]
//	cold start                    → [Build]
func DecideLaunchActions(in LaunchInputs) []LaunchAction {
	switch {
	case in.DryRun:
		return []LaunchAction{ActionDryRun}
	case in.ExplicitBuild:
		return []LaunchAction{ActionBuild}
	case in.VMRunning:
		return []LaunchAction{ActionAttach}
	case in.DiskExists && diskValid(in.DiskSizeBytes) && in.Provisioned:
		return []LaunchAction{ActionUseLocal}
	case in.DiskExists && (!diskValid(in.DiskSizeBytes) || !in.Provisioned):
		return []LaunchAction{ActionBuild}
	case in.TemplateExists && diskValid(in.TemplateSizeBytes):
		return []LaunchAction{ActionClone}
	default:
		return []LaunchAction{ActionBuild}
	}
}
