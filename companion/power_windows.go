//go:build windows

package main

import (
	"errors"
	"fmt"
	"unsafe"

	"golang.org/x/sys/windows"
)

type powerAction string

const (
	powerShutdown  powerAction = "shutdown"
	powerSleep     powerAction = "sleep"
	powerHibernate powerAction = "hibernate"
)

var errPowerUnsupported = errors.New("power state unavailable in Windows")

var powerProfile = windows.NewLazySystemDLL("powrprof.dll")
var getPowerCapabilities = powerProfile.NewProc("GetPwrCapabilities")
var setSuspendState = powerProfile.NewProc("SetSuspendState")

// SYSTEM_POWER_CAPABILITIES from the Windows 10+ SDK (76 bytes).
type systemPowerCapabilities struct {
	Buttons                                                               [3]byte
	SystemS1, SystemS2, SystemS3, SystemS4, SystemS5                      byte
	HiberFilePresent                                                      byte
	Features                                                              [7]byte
	ProcessorMaxThrottle, FastSystemS4, Hiberboot, WakeAlarmPresent, AoAc byte
	DiskSpinDown, HiberFileType, AoAcConnectivitySupported                byte
	Spare                                                                 [6]byte
	SystemBatteriesPresent, BatteriesAreShortTerm                         byte
	BatteryScale                                                          [6]uint32
	WakeStates                                                            [5]uint32
}

func readPowerCapabilities() (systemPowerCapabilities, error) {
	var capabilities systemPowerCapabilities
	result, _, err := getPowerCapabilities.Call(uintptr(unsafe.Pointer(&capabilities)))
	if result == 0 {
		return capabilities, fmt.Errorf("GetPwrCapabilities: %w", err)
	}
	return capabilities, nil
}

func (c systemPowerCapabilities) allows(action powerAction) bool {
	if action == powerSleep {
		return c.SystemS1 != 0 || c.SystemS2 != 0 || c.SystemS3 != 0 || c.AoAc != 0
	}
	if action == powerHibernate {
		return c.SystemS4 != 0 && c.HiberFilePresent != 0 && c.HiberFileType == 2
	}
	return false
}

func enableShutdownPrivilege() error {
	var token windows.Token
	if err := windows.OpenProcessToken(windows.CurrentProcess(), windows.TOKEN_ADJUST_PRIVILEGES|windows.TOKEN_QUERY, &token); err != nil {
		return err
	}
	defer token.Close()
	var luid windows.LUID
	name, _ := windows.UTF16PtrFromString("SeShutdownPrivilege")
	if err := windows.LookupPrivilegeValue(nil, name, &luid); err != nil {
		return err
	}
	privileges := windows.Tokenprivileges{PrivilegeCount: 1, Privileges: [1]windows.LUIDAndAttributes{{Luid: luid, Attributes: windows.SE_PRIVILEGE_ENABLED}}}
	if err := windows.AdjustTokenPrivileges(token, false, &privileges, 0, nil, nil); err != nil {
		return err
	}
	// AdjustTokenPrivileges can return success without granting a missing privilege.
	var size uint32
	if err := windows.GetTokenInformation(token, windows.TokenPrivileges, nil, 0, &size); !errors.Is(err, windows.ERROR_INSUFFICIENT_BUFFER) {
		return fmt.Errorf("query privileges: %w", err)
	}
	buffer := make([]byte, size)
	if err := windows.GetTokenInformation(token, windows.TokenPrivileges, &buffer[0], size, &size); err != nil {
		return err
	}
	actual := (*windows.Tokenprivileges)(unsafe.Pointer(&buffer[0]))
	for _, privilege := range actual.AllPrivileges() {
		if privilege.Luid == luid && privilege.Attributes&windows.SE_PRIVILEGE_ENABLED != 0 {
			return nil
		}
	}
	return windows.ERROR_NOT_ALL_ASSIGNED
}

func prepareSuspend(action powerAction) (func() error, error) {
	capabilities, err := readPowerCapabilities()
	if err != nil {
		return nil, err
	}
	if !capabilities.allows(action) {
		return nil, fmt.Errorf("%w: %s; inspect powercfg /a", errPowerUnsupported, action)
	}
	if err := enableShutdownPrivilege(); err != nil {
		return nil, fmt.Errorf("enable SeShutdownPrivilege: %w", err)
	}
	return func() error {
		var hibernate uintptr
		if action == powerHibernate {
			hibernate = 1
		}
		// Keep wake events enabled. Never use rundll32: its calling convention differs.
		result, _, err := setSuspendState.Call(hibernate, 0, 0)
		if result == 0 {
			return fmt.Errorf("SetSuspendState(%s): %w", action, err)
		}
		return nil
	}, nil
}
