package tart_test

import (
	"fmt"
	"testing"

	"github.com/DimmKirr/devcell/internal/vm/tart"
)

func equalActions(a, b []tart.LaunchAction) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func TestDecideLaunch_DiskExists(t *testing.T) {
	got := tart.DecideLaunchActions(tart.LaunchInputs{DiskExists: true})
	want := []tart.LaunchAction{tart.ActionUseLocal}
	if !equalActions(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
}

func TestDecideLaunch_DryRun(t *testing.T) {
	// DryRun takes precedence over DiskExists.
	got := tart.DecideLaunchActions(tart.LaunchInputs{DryRun: true, DiskExists: true})
	want := []tart.LaunchAction{tart.ActionDryRun}
	if !equalActions(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
}

func TestDecideLaunch_ExplicitBuild(t *testing.T) {
	got := tart.DecideLaunchActions(tart.LaunchInputs{ExplicitBuild: true})
	want := []tart.LaunchAction{tart.ActionBuild}
	if !equalActions(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
}

func TestDecideLaunch_ExplicitBuild_OverridesDisk(t *testing.T) {
	got := tart.DecideLaunchActions(tart.LaunchInputs{ExplicitBuild: true, DiskExists: true})
	want := []tart.LaunchAction{tart.ActionBuild}
	if !equalActions(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
}

func TestDecideLaunch_ColdStart(t *testing.T) {
	got := tart.DecideLaunchActions(tart.LaunchInputs{DiskExists: false})
	want := []tart.LaunchAction{tart.ActionBuild}
	if !equalActions(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
}

func TestDecideLaunch_DryRunOverridesAll(t *testing.T) {
	got := tart.DecideLaunchActions(tart.LaunchInputs{
		DryRun:        true,
		ExplicitBuild: true,
		DiskExists:    true,
	})
	want := []tart.LaunchAction{tart.ActionDryRun}
	if !equalActions(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
}

func TestDecideLaunch_TartRef_ColdStart(t *testing.T) {
	got := tart.DecideLaunchActions(tart.LaunchInputs{
		TartRef: "ghcr.io/cirruslabs/macos-sequoia-base:latest",
	})
	want := []tart.LaunchAction{tart.ActionPullTart}
	if !equalActions(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
}

func TestDecideLaunch_TartRef_DiskExists(t *testing.T) {
	got := tart.DecideLaunchActions(tart.LaunchInputs{
		TartRef:    "ghcr.io/cirruslabs/macos-sequoia-base:latest",
		DiskExists: true,
	})
	want := []tart.LaunchAction{tart.ActionUseLocal}
	if !equalActions(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
}

func TestDecideLaunch_TartRef_ExplicitBuild(t *testing.T) {
	got := tart.DecideLaunchActions(tart.LaunchInputs{
		TartRef:       "ghcr.io/cirruslabs/macos-sequoia-base:latest",
		ExplicitBuild: true,
	})
	want := []tart.LaunchAction{tart.ActionBuild}
	if !equalActions(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
}

func TestDecideLaunch_TartRef_DryRun(t *testing.T) {
	got := tart.DecideLaunchActions(tart.LaunchInputs{
		TartRef: "ghcr.io/cirruslabs/macos-sequoia-base:latest",
		DryRun:  true,
	})
	want := []tart.LaunchAction{tart.ActionDryRun}
	if !equalActions(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
}

func TestDecideLaunch_Table(t *testing.T) {
	tests := []struct {
		name string
		in   tart.LaunchInputs
		want []tart.LaunchAction
	}{
		{
			name: "all false → cold start → Build",
			in:   tart.LaunchInputs{DryRun: false, ExplicitBuild: false, DiskExists: false},
			want: []tart.LaunchAction{tart.ActionBuild},
		},
		{
			name: "DiskExists only → UseLocal",
			in:   tart.LaunchInputs{DryRun: false, ExplicitBuild: false, DiskExists: true},
			want: []tart.LaunchAction{tart.ActionUseLocal},
		},
		{
			name: "ExplicitBuild only → Build",
			in:   tart.LaunchInputs{DryRun: false, ExplicitBuild: true, DiskExists: false},
			want: []tart.LaunchAction{tart.ActionBuild},
		},
		{
			name: "ExplicitBuild + DiskExists → Build (ExplicitBuild wins)",
			in:   tart.LaunchInputs{DryRun: false, ExplicitBuild: true, DiskExists: true},
			want: []tart.LaunchAction{tart.ActionBuild},
		},
		{
			name: "DryRun only → DryRun",
			in:   tart.LaunchInputs{DryRun: true, ExplicitBuild: false, DiskExists: false},
			want: []tart.LaunchAction{tart.ActionDryRun},
		},
		{
			name: "DryRun + DiskExists → DryRun (DryRun wins)",
			in:   tart.LaunchInputs{DryRun: true, ExplicitBuild: false, DiskExists: true},
			want: []tart.LaunchAction{tart.ActionDryRun},
		},
		{
			name: "DryRun + ExplicitBuild → DryRun (DryRun wins)",
			in:   tart.LaunchInputs{DryRun: true, ExplicitBuild: true, DiskExists: false},
			want: []tart.LaunchAction{tart.ActionDryRun},
		},
		{
			name: "all true → DryRun (DryRun wins)",
			in:   tart.LaunchInputs{DryRun: true, ExplicitBuild: true, DiskExists: true},
			want: []tart.LaunchAction{tart.ActionDryRun},
		},
		{
			name: "TartRef cold start → PullTart",
			in:   tart.LaunchInputs{TartRef: "ghcr.io/cirruslabs/macos-sequoia-base:latest"},
			want: []tart.LaunchAction{tart.ActionPullTart},
		},
		{
			name: "TartRef + DiskExists → UseLocal (already pulled)",
			in:   tart.LaunchInputs{TartRef: "ghcr.io/cirruslabs/macos-sequoia-base:latest", DiskExists: true},
			want: []tart.LaunchAction{tart.ActionUseLocal},
		},
		{
			name: "TartRef + ExplicitBuild → Build (ExplicitBuild wins)",
			in:   tart.LaunchInputs{TartRef: "ghcr.io/cirruslabs/macos-sequoia-base:latest", ExplicitBuild: true},
			want: []tart.LaunchAction{tart.ActionBuild},
		},
		{
			name: "TartRef + DryRun → DryRun (DryRun wins)",
			in:   tart.LaunchInputs{TartRef: "ghcr.io/cirruslabs/macos-sequoia-base:latest", DryRun: true},
			want: []tart.LaunchAction{tart.ActionDryRun},
		},
	}
	for i, tt := range tests {
		t.Run(fmt.Sprintf("%d_%s", i, tt.name), func(t *testing.T) {
			got := tart.DecideLaunchActions(tt.in)
			if !equalActions(got, tt.want) {
				t.Errorf("DecideLaunchActions(%+v) = %v, want %v", tt.in, got, tt.want)
			}
		})
	}
}
