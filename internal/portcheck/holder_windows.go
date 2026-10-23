//go:build windows

package portcheck

import (
	"fmt"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
)

// mibTCPRowOwnerPID mirrors MIB_TCPROW_OWNER_PID from iphlpapi.
type mibTCPRowOwnerPID struct {
	State      uint32
	LocalAddr  uint32
	LocalPort  uint32 // network byte order in the low 16 bits
	RemoteAddr uint32
	RemotePort uint32
	OwningPID  uint32
}

const tcpTableOwnerPIDListener = 3 // TCP_TABLE_OWNER_PID_LISTENER

// x/sys/windows does not wrap GetExtendedTcpTable, so bind it directly.
var (
	iphlpapi                = windows.NewLazySystemDLL("iphlpapi.dll")
	procGetExtendedTCPTable = iphlpapi.NewProc("GetExtendedTcpTable")
)

func getExtendedTCPTable(table uintptr, size *uint32, order bool, af uint32, class uint32) error {
	ord := uintptr(0)
	if order {
		ord = 1
	}
	r0, _, _ := procGetExtendedTCPTable.Call(table, uintptr(unsafe.Pointer(size)), ord, uintptr(af), uintptr(class), 0)
	if r0 != 0 {
		return syscall.Errno(r0)
	}
	return nil
}

// findHolder returns the PID/image of the process listening on port, using
// GetExtendedTcpTable (the same source netstat -ano reads from, without the
// text parsing).
func findHolder(port int) (*Holder, error) {
	var size uint32
	// First call sizes the buffer; ERROR_INSUFFICIENT_BUFFER is expected.
	_ = getExtendedTCPTable(0, &size, true, syscall.AF_INET, tcpTableOwnerPIDListener)
	if size == 0 {
		return nil, fmt.Errorf("portcheck: GetExtendedTcpTable returned zero size")
	}
	buf := make([]byte, size)
	if err := getExtendedTCPTable(uintptr(unsafe.Pointer(&buf[0])), &size, true, syscall.AF_INET, tcpTableOwnerPIDListener); err != nil {
		return nil, fmt.Errorf("portcheck: GetExtendedTcpTable: %w", err)
	}

	count := *(*uint32)(unsafe.Pointer(&buf[0]))
	rows := unsafe.Slice((*mibTCPRowOwnerPID)(unsafe.Pointer(&buf[4])), count)
	for _, r := range rows {
		localPort := int(r.LocalPort&0xFF)<<8 | int(r.LocalPort>>8&0xFF)
		if localPort != port {
			continue
		}
		pid := int(r.OwningPID)
		return &Holder{PID: pid, Image: imageName(uint32(pid))}, nil
	}
	return nil, fmt.Errorf("portcheck: no listener found on port %d", port)
}

// imageName resolves a PID to its executable name via a Toolhelp snapshot.
// Best-effort: returns "" when the process cannot be inspected.
func imageName(pid uint32) string {
	snap, err := windows.CreateToolhelp32Snapshot(windows.TH32CS_SNAPPROCESS, 0)
	if err != nil {
		return ""
	}
	defer windows.CloseHandle(snap)
	var pe windows.ProcessEntry32
	pe.Size = uint32(unsafe.Sizeof(pe))
	for err := windows.Process32First(snap, &pe); err == nil; err = windows.Process32Next(snap, &pe) {
		if pe.ProcessID == pid {
			return windows.UTF16ToString(pe.ExeFile[:])
		}
	}
	return ""
}
