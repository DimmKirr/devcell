//go:build windows

// hcsboot is the nested-VM smoke test that runs inside a booted
// devcell.wim. It creates a diskless Gen2 VM through the Host Compute
// System API — the same path WSL2 uses — and reports the VM's state as
// KEY=VALUE markers the host test asserts on:
//
//	HCSBOOT_CREATE=OK|<hresult and result document>
//	HCSBOOT_START=OK|<hresult and result document>
//	HCSBOOT_STATE=Running|<other>
//	HCSBOOT_DONE=1
//
// It calls computecore.dll directly rather than pulling in hcsshim: the
// four calls needed are trivial, and hcsshim's compute-system client lives
// in an internal package anyway.
package main

import (
	"encoding/json"
	"fmt"
	"os"
	"time"
	"unsafe"

	"github.com/DimmKirr/devcell/internal/hcsvm"
	"golang.org/x/sys/windows"
)

var (
	computecore = windows.NewLazySystemDLL("computecore.dll")

	procCreateOperation        = computecore.NewProc("HcsCreateOperation")
	procWaitForOperationResult = computecore.NewProc("HcsWaitForOperationResult")
	procCreateComputeSystem    = computecore.NewProc("HcsCreateComputeSystem")
	procStartComputeSystem     = computecore.NewProc("HcsStartComputeSystem")
	procGetProperties          = computecore.NewProc("HcsGetComputeSystemProperties")
	procTerminateComputeSystem = computecore.NewProc("HcsTerminateComputeSystem")
)

// waitOp blocks on the operation and returns its result document. A failed
// HCS call reports its real error through this document, not the HRESULT.
func waitOp(op uintptr, timeout time.Duration) (string, error) {
	var resultDoc *uint16
	hr, _, _ := procWaitForOperationResult.Call(op,
		uintptr(timeout.Milliseconds()), uintptr(unsafe.Pointer(&resultDoc)))
	doc := ""
	if resultDoc != nil {
		doc = windows.UTF16PtrToString(resultDoc)
	}
	if hr != 0 {
		return doc, fmt.Errorf("hresult=0x%08x result=%s", uint32(hr), doc)
	}
	return doc, nil
}

func main() {
	fail := func(stage string, err error) {
		fmt.Printf("HCSBOOT_%s=%v\n", stage, err)
		fmt.Println("HCSBOOT_DONE=1")
		os.Exit(1)
	}

	id, _ := windows.UTF16PtrFromString("devcell-hcs-test")
	config, _ := windows.UTF16PtrFromString(hcsvm.VMDocJSON(512, 1))

	op, _, _ := procCreateOperation.Call(0, 0)
	if op == 0 {
		fail("CREATE", fmt.Errorf("HcsCreateOperation returned NULL"))
	}

	var system uintptr
	hr, _, _ := procCreateComputeSystem.Call(
		uintptr(unsafe.Pointer(id)),
		uintptr(unsafe.Pointer(config)),
		op, 0, uintptr(unsafe.Pointer(&system)))
	if hr != 0 {
		fail("CREATE", fmt.Errorf("hresult=0x%08x", uint32(hr)))
	}
	// Nested under TCG everything is slow; be generous.
	if doc, err := waitOp(op, 10*time.Minute); err != nil {
		fail("CREATE", err)
	} else {
		_ = doc
		fmt.Println("HCSBOOT_CREATE=OK")
	}

	hr, _, _ = procStartComputeSystem.Call(system, op, 0)
	if hr != 0 {
		fail("START", fmt.Errorf("hresult=0x%08x", uint32(hr)))
	}
	if _, err := waitOp(op, 10*time.Minute); err != nil {
		fail("START", err)
	}
	fmt.Println("HCSBOOT_START=OK")

	hr, _, _ = procGetProperties.Call(system, op, 0)
	if hr != 0 {
		fail("STATE", fmt.Errorf("hresult=0x%08x", uint32(hr)))
	}
	propDoc, err := waitOp(op, 2*time.Minute)
	if err != nil {
		fail("STATE", err)
	}
	var props struct {
		State string `json:"State"`
	}
	if err := json.Unmarshal([]byte(propDoc), &props); err != nil {
		fail("STATE", fmt.Errorf("unparseable properties: %v: %s", err, propDoc))
	}
	fmt.Printf("HCSBOOT_STATE=%s\n", props.State)

	// Leave the firmware on screen long enough for a thumbnail.
	time.Sleep(10 * time.Second)

	opts, _ := windows.UTF16PtrFromString("")
	procTerminateComputeSystem.Call(system, op, uintptr(unsafe.Pointer(opts)))
	waitOp(op, time.Minute)

	fmt.Println("HCSBOOT_DONE=1")
}
