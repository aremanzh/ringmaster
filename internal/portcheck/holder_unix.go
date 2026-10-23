//go:build !windows

package portcheck

import (
	"os/exec"
	"strconv"
	"strings"
)

// findHolder looks up who is listening on port via lsof (present by default
// on macOS and on virtually every Linux dev box). Best-effort: if lsof is
// missing or returns nothing, the caller still reports "port busy", just
// without the PID/image detail.
func findHolder(port int) (*Holder, error) {
	out, err := exec.Command(
		"lsof", "-nP", "-iTCP:"+strconv.Itoa(port), "-sTCP:LISTEN", "-Fpc",
	).Output()
	if err != nil {
		// lsof exits 1 when nothing matches; treat every failure as "unknown".
		return nil, nil
	}
	// -F output: lines like "p1234" (pid) then "cphp" (command).
	h := &Holder{}
	for _, line := range strings.Split(string(out), "\n") {
		switch {
		case strings.HasPrefix(line, "p") && h.PID == 0:
			if pid, err := strconv.Atoi(line[1:]); err == nil {
				h.PID = pid
			}
		case strings.HasPrefix(line, "c") && h.Image == "":
			h.Image = strings.TrimSpace(line[1:])
		}
	}
	if h.PID == 0 {
		return nil, nil
	}
	return h, nil
}
