package supervisor

import (
	"fmt"
	"os/exec"
	"strings"
	"sync"
	"time"
)

// dockerCheckTTL caches the preflight result briefly so starting several
// docker-dependent tasks does not shell out once per task.
const dockerCheckTTL = 5 * time.Second

var dockerMu sync.Mutex
var dockerCheckedAt time.Time
var dockerCheckErr error

// checkDocker verifies the Docker engine is reachable. It is the preflight
// behind TaskSpec.RequiresDocker: `docker version` talks to the daemon, so a
// stopped Docker Desktop / dockerd fails here with a clear message instead
// of the task crash-looping.
func checkDocker() error {
	dockerMu.Lock()
	defer dockerMu.Unlock()
	if time.Since(dockerCheckedAt) < dockerCheckTTL {
		return dockerCheckErr
	}
	dockerCheckedAt = time.Now()

	if _, err := exec.LookPath("docker"); err != nil {
		dockerCheckErr = fmt.Errorf("docker CLI not found on PATH")
		return dockerCheckErr
	}
	cmd := exec.Command("docker", "version", "--format", "{{.Server.Version}}")
	out, err := cmd.Output()
	if err != nil || strings.TrimSpace(string(out)) == "" {
		dockerCheckErr = fmt.Errorf("docker engine not reachable — is Docker Desktop/dockerd running?")
		return dockerCheckErr
	}
	dockerCheckErr = nil
	return nil
}
