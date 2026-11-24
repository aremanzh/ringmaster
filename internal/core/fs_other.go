//go:build !windows

package core

// fsRoots is the single root of a Unix file system.
func fsRoots() []string { return []string{"/"} }
