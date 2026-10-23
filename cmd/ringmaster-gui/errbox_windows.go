//go:build gui && windows

package main

import "golang.org/x/sys/windows"

// showError surfaces a fatal startup error in a message box. The GUI is linked
// with -H windowsgui, so there is no console and stderr would be invisible.
func showError(msg string) {
	title, _ := windows.UTF16PtrFromString("Ringmaster")
	text, _ := windows.UTF16PtrFromString(msg)
	windows.MessageBox(0, text, title, windows.MB_OK|windows.MB_ICONERROR)
}
