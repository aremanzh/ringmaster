//go:build gui && !windows

package main

// showError is a no-op off Windows; the error already went to stderr.
func showError(string) {}
