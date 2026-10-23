//go:build windows

package supervisor

import (
	"unicode/utf16"
	"unicode/utf8"

	"golang.org/x/sys/windows"
)

// decodeConsole converts one raw console output line to UTF-8. Windows
// console programs often emit the active ANSI/OEM codepage instead of UTF-8
// (e.g. cp1252 or cp850 accented characters). Valid UTF-8 passes through
// untouched; anything else is decoded from the console output codepage via
// MultiByteToWideChar, falling back to the raw bytes if conversion fails.
func decodeConsole(b []byte) string {
	if utf8.Valid(b) {
		return string(b)
	}
	cp, err := windows.GetConsoleOutputCP()
	if err != nil || cp == 0 {
		cp = windows.GetACP()
	}
	n, err := windows.MultiByteToWideChar(cp, 0, &b[0], int32(len(b)), nil, 0)
	if err != nil || n <= 0 {
		return string(b)
	}
	buf := make([]uint16, n)
	if _, err := windows.MultiByteToWideChar(cp, 0, &b[0], int32(len(b)), &buf[0], n); err != nil {
		return string(b)
	}
	return string(utf16.Decode(buf))
}
