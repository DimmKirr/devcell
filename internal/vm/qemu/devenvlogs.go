package qemu

import (
	"fmt"
	"strings"

	"github.com/DimmKirr/devcell/internal/isokit"
)

// The guest log volume: a FAT image any post-install VM can write logs to —
// the same channel the install's answer volume provides, for the same reason.
// The SSH stream dies with its connection, while FAT survives anything short
// of losing the image file. The guest finds the volume by marker file, never
// by drive letter.
const GuestLogVolumeMarker = "devcell-guest-logs.txt"

// BuildGuestLogVolume creates the FAT image guests write their logs to.
// Attach it via Spec.LogVolumePath; read it back with CollectVolumeLogs.
func BuildGuestLogVolume(destPath string) error {
	return BuildControlVolume(destPath, nil)
}

// BuildControlVolume writes the per-run control volume: the marker the guest
// resolves its drive letter by, plus any payload to deliver INTO the guest
// (the PowerShell module and stage scripts — see CELL-402). Logs come back
// on the same volume, so one attachment carries both directions.
//
// Built fresh on the host every run and attached at boot, so it is never
// inside the qcow2: a checkpoint image cannot freeze a stale copy, which is
// the failure mode that ruled out installing the module onto the guest disk.
func BuildControlVolume(destPath string, payload map[string][]byte) error {
	files := map[string][]byte{
		"/" + GuestLogVolumeMarker: padForFAT([]byte("devcell guest control volume\r\n")),
	}
	for name, data := range payload {
		files[name] = padForFAT(data)
	}
	if err := isokit.CreateFATImage(destPath, files); err != nil {
		return fmt.Errorf("building control volume: %w", err)
	}
	return nil
}

// CollectVolumeLogs reads the named files off a guest log volume — one entry
// per name, absence reported rather than skipped, same contract as
// CollectGuestLogs.
func CollectVolumeLogs(imgPath string, names []string) []GuestLog {
	logs := make([]GuestLog, 0, len(names))
	for _, name := range names {
		data, err := isokit.ReadFileFromFAT(imgPath, "/"+name)
		if err != nil {
			logs = append(logs, GuestLog{Name: name, Err: fmt.Errorf("%w: %v", errNoSuchGuestLog, err)})
			continue
		}
		logs = append(logs, GuestLog{Name: name, Content: data})
	}
	return logs
}

// StageLogName is the transcript filename for a dev-env component,
// prefixed with the component's 1-based position in the pipeline. Grouping by
// component rather than by SSH execution means "what happened with WSL" is one
// file covering the feature, the engine and the distro import; the number
// keeps a results directory sorted in execution order.
func StageLogName(seq int, component string) string {
	return fmt.Sprintf("%03d-devenv-%s.log", seq, strings.ReplaceAll(component, " ", "-"))
}

// withStageLogging wraps every stage in the table so it transcripts into its
// component's log on the guest log volume. One call at the end of a table
// builder is the whole contract — every guest pipeline gets identical logging
// without its stage definitions mentioning logs at all.
func withStageLogging(stages []GuestStage) []GuestStage {
	names := StageLogNames(stages)
	for i := range stages {
		if stages[i].ScriptFile != "" {
			// File-backed stages own their logging (Initialize-DevcellLogging
			// inside the script); wrapping them would double it. Pass the
			// component log name through as a parameter instead.
			if stages[i].Args == nil {
				stages[i].Args = map[string]string{}
			}
			stages[i].Args["LogName"] = names[i]
			continue
		}
		stages[i].Script = withLogVolumeTranscript(names[i], stages[i].Name, stages[i].Script)
	}
	return stages
}

// StageLogNames maps each stage to its component's log name, numbering
// components by first appearance. Stages sharing a component share a file.
func StageLogNames(stages []GuestStage) []string {
	seq := map[string]int{}
	carried := map[string]string{}
	names := make([]string, len(stages))
	for i, st := range stages {
		// A stage that was TOLD which log to write (file-backed stages carry
		// Args["LogName"]) is authoritative: the host must read exactly the
		// file the guest writes. Renumbering a span independently once had
		// the guest writing 004-devenv-WSL.log while the host wrote 001.
		if given := st.Args["LogName"]; given != "" {
			carried[st.Component] = given
		}
		if _, seen := seq[st.Component]; !seen {
			seq[st.Component] = len(seq) + 1
		}
		names[i] = StageLogName(seq[st.Component], st.Component)
	}
	for i, st := range stages {
		if given := carried[st.Component]; given != "" {
			names[i] = given
		}
	}
	return names
}

// withLogVolumeTranscript wraps a stage script so it transcripts itself onto
// the guest log volume. Best-effort by design: a missing volume must never
// fail a stage, and the wrapper must not swallow the script's own throw —
// finally preserves propagation.
// withLogVolumeTranscript wraps a stage script so it appends to its
// component's transcript on the guest log volume. $ProgressPreference is
// silenced first: Invoke-WebRequest's progress records travel over SSH as
// CLIXML and turned two stage logs into 8.9MB and 11.8MB of noise.
func withLogVolumeTranscript(logName, stageName, script string) string {
	return renderTemplate("stage-wrapper.ps1.tmpl", struct {
		Marker    string
		LogName   string
		StageName string
		Script    string
	}{GuestLogVolumeMarker, logName, stageName, script})
}
