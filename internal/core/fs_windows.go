//go:build windows

package core

import "golang.org/x/sys/windows"

// fsRoots lists the drives that exist, from the system's drive bitmask, so no
// drive is touched (an empty card reader would otherwise stall the listing).
func fsRoots() []string {
	mask, err := windows.GetLogicalDrives()
	if err != nil {
		return []string{`C:\`}
	}
	var roots []string
	for i := 0; i < 26; i++ {
		if mask&(1<<uint(i)) != 0 {
			roots = append(roots, string(rune('A'+i))+`:\`)
		}
	}
	return roots
}
