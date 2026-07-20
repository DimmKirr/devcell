//go:build darwin && arm64

package tart

import (
	"context"
	"fmt"
	"time"
)

// AcquireDarwinVM resolves VM availability and returns a result
// the caller uses to connect to the VM. Four cases:
//
//  1. External VM: just wait for SSH.
//  2. Managed VM already running: no-op.
//  3. Instance missing but template exists: clone template → start.
//  4. Neither exists: call InitFunc (full build) → start.
//
// The caller must call result.VM.Stop() when done if result.Managed is true.
func AcquireDarwinVM(ctx context.Context, in AcquireInputs) (*AcquireResult, error) {
	in.ApplyDefaults()
	if err := in.Validate(); err != nil {
		return nil, err
	}

	if in.ExternalVM {
		if err := WaitForSSH(in.SSHHost, in.SSHPort, in.SSHTimeout, 2*time.Second); err != nil {
			return nil, fmt.Errorf("external VM not reachable: %w", err)
		}
		return &AcquireResult{
			SSHHost: in.SSHHost,
			SSHPort: in.SSHPort,
			Managed: false,
		}, nil
	}

	// Check if instance VM exists / is already running.
	// If not, try cloning from a built template before falling back to full build.
	info, err := TartGet(ctx, in.VMName)
	if err != nil {
		if in.TemplateName != "" {
			if _, tmplErr := TartGet(ctx, in.TemplateName); tmplErr == nil {
				if cloneErr := TartClone(ctx, in.TemplateName, in.VMName); cloneErr != nil {
					return nil, fmt.Errorf("cloning template %s → %s: %w", in.TemplateName, in.VMName, cloneErr)
				}
				info, err = TartGet(ctx, in.VMName)
				if err != nil {
					return nil, fmt.Errorf("VM %s not found after cloning from template: %w", in.VMName, err)
				}
				goto resolved
			}
		}
		if in.InitFunc == nil {
			return nil, fmt.Errorf("VM %s not found (run 'cell build --engine=tart' first): %w", in.VMName, err)
		}
		if initErr := in.InitFunc(); initErr != nil {
			return nil, fmt.Errorf("auto-build failed: %w", initErr)
		}
		// InitFunc builds the template — clone it to the instance VM.
		if in.TemplateName != "" {
			if cloneErr := TartClone(ctx, in.TemplateName, in.VMName); cloneErr != nil {
				return nil, fmt.Errorf("cloning template %s → %s after build: %w", in.TemplateName, in.VMName, cloneErr)
			}
		}
		info, err = TartGet(ctx, in.VMName)
		if err != nil {
			return nil, fmt.Errorf("VM %s still not found after auto-build: %w", in.VMName, err)
		}
	}
resolved:
	if info.Running {
		ip, err := TartIP(ctx, in.VMName)
		if err != nil {
			return nil, fmt.Errorf("VM %s is running but IP not available: %w", in.VMName, err)
		}
		return &AcquireResult{
			SSHHost: ip,
			SSHPort: in.SSHPort,
			Managed: false,
		}, nil
	}

	// Start VM
	vm, err := TartRun(ctx, in.VMName, in.SharedDirs, in.Disks)
	if err != nil {
		return nil, fmt.Errorf("starting VM %s: %w", in.VMName, err)
	}

	// Wait for IP
	ip, err := vm.WaitForIP(ctx, in.SSHTimeout, 3*time.Second)
	if err != nil {
		vm.ForceStop()
		return nil, fmt.Errorf("VM %s started but IP not discovered: %w", in.VMName, err)
	}

	return &AcquireResult{
		VM:      vm,
		SSHHost: ip,
		SSHPort: in.SSHPort,
		Managed: true,
	}, nil
}
