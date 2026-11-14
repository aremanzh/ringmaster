package main

import (
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"

	"github.com/aremanzh/ringmaster/internal/daemon"
	"github.com/aremanzh/ringmaster/internal/discover"
	"github.com/aremanzh/ringmaster/internal/supervisor"
)

// exitCode is returned by runCommand when the command itself exited
// non-zero; main exits with the same code and prints nothing.
type exitCode int

func (e exitCode) Error() string { return fmt.Sprintf("exit status %d", int(e)) }

// runCommand runs a one-off command for a project: in the project folder,
// with the project's PHP folder first on PATH, attached to this terminal.
// It is `php artisan migrate`, `composer install` or `php artisan tinker`
// without having to remember which php.exe the project uses. It does not
// involve the daemon and is not supervised.
func (a *app) runCommand(rest []string, stdin io.Reader, stdout, stderr io.Writer) error {
	const usage = "usage: ringmaster run <projectId> [--] <command> [args...]"
	if len(rest) == 0 {
		return errors.New(usage)
	}
	p, err := a.project(rest[0])
	if err != nil {
		return err
	}
	argv := rest[1:]
	if len(argv) > 0 && argv[0] == "--" {
		argv = argv[1:]
	}
	if len(argv) == 0 {
		return errors.New(usage)
	}
	if fi, err := os.Stat(p.Path); err != nil || !fi.IsDir() {
		return fmt.Errorf("project %s: path %q does not exist or is not a directory", p.ID, p.Path)
	}

	installs, err := discover.Discover(a.cfg.PHPRoots)
	if err != nil {
		return err
	}
	phpPath, err := daemon.ProjectPHP(p, installs)
	if err != nil {
		return err
	}
	prepend := daemon.PHPPath(phpPath)

	argv, err = supervisor.CommandArgv(argv, prepend)
	if err != nil {
		return err
	}
	cmd := exec.Command(argv[0], argv[1:]...)
	cmd.Dir = p.Path
	cmd.Env = supervisor.CommandEnv(prepend)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = stdin, stdout, stderr

	// Ctrl+C reaches the command directly, since it shares this terminal;
	// what it does with it (tinker clears the line) is the command's call,
	// so ringmaster itself must not die first.
	signal.Ignore(os.Interrupt)
	defer signal.Reset(os.Interrupt)

	if err := cmd.Run(); err != nil {
		var exit *exec.ExitError
		if errors.As(err, &exit) {
			return exitCode(exit.ExitCode())
		}
		return fmt.Errorf("run %s: %w", argv[0], err)
	}
	return nil
}
