// Package portcheck answers two questions before a primary task starts:
// "is this port free?" and, if not, "which PID/image is holding it?".
package portcheck

import (
	"fmt"
	"net"
)

// Holder identifies the process occupying a port.
type Holder struct {
	PID   int
	Image string // executable name, e.g. "php.exe"; may be empty if unknown
}

// ErrPortBusy is returned by Free when the port is occupied.
type ErrPortBusy struct {
	Host   string
	Port   int
	Holder *Holder // nil if the holder could not be identified
}

func (e *ErrPortBusy) Error() string {
	if e.Holder != nil {
		return fmt.Sprintf("port %d on %s is in use by PID %d (%s)", e.Port, e.Host, e.Holder.PID, e.Holder.Image)
	}
	return fmt.Sprintf("port %d on %s is in use (holder unknown)", e.Port, e.Host)
}

// Free verifies host:port can be bound. If not, it returns *ErrPortBusy
// carrying the holding PID/image when identifiable.
func Free(host string, port int) error {
	l, err := net.Listen("tcp", fmt.Sprintf("%s:%d", host, port))
	if err == nil {
		l.Close()
		return nil
	}
	h, herr := findHolder(port)
	if herr != nil {
		h = nil // best-effort: report busy even if the holder lookup failed
	}
	return &ErrPortBusy{Host: host, Port: port, Holder: h}
}

// FreeInRange returns the first bindable port in the inclusive [lo, hi]
// range on host.
func FreeInRange(host string, lo, hi int) (int, error) {
	return FreeInRangeExcept(host, lo, hi, nil)
}

// FreeInRangeExcept returns the first bindable port in the inclusive
// [lo, hi] range on host that is not in skip. skip carries ports already
// promised to other projects in the same run: a bind check alone cannot see
// them because their tasks have not started listening yet.
func FreeInRangeExcept(host string, lo, hi int, skip map[int]bool) (int, error) {
	for p := lo; p <= hi; p++ {
		if skip[p] {
			continue
		}
		l, err := net.Listen("tcp", fmt.Sprintf("%s:%d", host, p))
		if err == nil {
			l.Close()
			return p, nil
		}
	}
	return 0, fmt.Errorf("portcheck: no free port in range %d-%d on %s", lo, hi, host)
}
