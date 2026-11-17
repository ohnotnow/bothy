package bothy

import (
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"

	"github.com/mattn/go-isatty"
)

func isTerminal(f *os.File) bool {
	return isatty.IsTerminal(f.Fd())
}

// runDocker runs docker attached to the terminal and returns its exit code.
// While it runs, bothy ignores ctrl-c: the terminal sends it to docker too,
// so docker stops (e.g. horizon) and bothy passes on its exit code.
func runDocker(args []string) int {
	return pipeDocker(args, os.Stdin, os.Stdout)
}

// pipeDocker is runDocker with stdin and stdout redirected.
func pipeDocker(args []string, stdin io.Reader, stdout io.Writer) int {
	cmd := exec.Command("docker", args...)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = stdin, stdout, os.Stderr

	sigs := make(chan os.Signal, 1)
	signal.Notify(sigs, os.Interrupt)
	defer signal.Stop(sigs)

	err := cmd.Run()
	var exitErr *exec.ExitError
	switch {
	case err == nil:
		return 0
	case errors.As(err, &exitErr):
		return exitErr.ExitCode()
	default:
		fmt.Fprintf(os.Stderr, "bothy: running docker: %v\n", err)
		return 127
	}
}

// findMkcert returns a function that runs mkcert, or nil if it isn't on PATH.
func findMkcert() func(args ...string) error {
	path, err := exec.LookPath("mkcert")
	if err != nil {
		return nil
	}
	return func(args ...string) error {
		cmd := exec.Command(path, args...)
		cmd.Stdout, cmd.Stderr = os.Stdout, os.Stderr
		return cmd.Run()
	}
}

func outputDocker(args []string) ([]byte, error) {
	cmd := exec.Command("docker", args...)
	cmd.Stderr = os.Stderr
	return cmd.Output()
}
