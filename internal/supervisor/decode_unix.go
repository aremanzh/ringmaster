//go:build !windows

package supervisor

// decodeConsole is a passthrough on Unix: dev tools emit UTF-8.
func decodeConsole(b []byte) string {
	return string(b)
}
