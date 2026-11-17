package bothy

import (
	"bufio"
	"fmt"
	"path/filepath"
	"strings"
)

const (
	appService  = "appserver"
	nodeService = "node"
	seeder      = "TestDataSeeder"
)

// compose prefixes args with the dev compose file.
func compose(args ...string) []string {
	return append([]string{"compose", "-f", ComposeFile}, args...)
}

// noTTY returns -T when there is no terminal, so agents and scripts work.
func (a *App) noTTY() []string {
	if a.Interactive {
		return nil
	}
	return []string{"-T"}
}

// inApp builds `compose exec appserver <args>`.
func (a *App) inApp(args ...string) []string {
	x := append([]string{"exec"}, a.noTTY()...)
	x = append(x, appService)
	return compose(append(x, args...)...)
}

func (a *App) artisan(args ...string) []string {
	return a.inApp(append([]string{"php", "artisan"}, args...)...)
}

func cmdStart(a *App, args []string) int {
	if len(args) > 0 {
		return a.usageError("start takes no arguments")
	}
	if code := a.Run(compose("up", "-d", "--build")); code != 0 || a.dryRun {
		return code
	}
	fmt.Fprintln(a.Stdout)
	return a.info()
}

func cmdStop(a *App, args []string) int {
	if len(args) > 0 {
		return a.usageError("stop takes no arguments")
	}
	return a.Run(compose("down"))
}

// runAll runs each docker command in turn, stopping at the first failure.
func (a *App) runAll(cmds ...[]string) int {
	for _, c := range cmds {
		if code := a.Run(c); code != 0 {
			return code
		}
	}
	return 0
}

func cmdRebuild(a *App, args []string) int {
	if len(args) > 0 {
		return a.usageError("rebuild takes no arguments")
	}
	if code := a.runAll(compose("down"), compose("build", "--pull"), compose("up", "-d")); code != 0 || a.dryRun {
		return code
	}
	fmt.Fprintln(a.Stdout)
	return a.info()
}

func cmdDestroy(a *App, args []string) int {
	if len(args) > 0 {
		return a.usageError("destroy takes no arguments")
	}
	if !a.dryRun {
		if !a.Interactive {
			fmt.Fprintln(a.Stderr, "bothy: destroy asks before deleting anything, so it needs a terminal")
			return 1
		}
		fmt.Fprintf(a.Stdout, "This stops %s and deletes its volumes, including the database. Are you sure? [y/N] ", filepath.Base(a.Dir))
		answer, _ := bufio.NewReader(a.Stdin).ReadString('\n')
		if ans := strings.ToLower(strings.TrimSpace(answer)); ans != "y" && ans != "yes" {
			fmt.Fprintln(a.Stdout, "Nothing deleted.")
			return 1
		}
	}
	return a.Run(compose("down", "-v"))
}

func cmdArtisan(a *App, args []string) int {
	return a.Run(a.artisan(args...))
}

func cmdTinker(a *App, args []string) int {
	return a.Run(a.artisan(append([]string{"tinker"}, args...)...))
}

func cmdComposer(a *App, args []string) int {
	return a.Run(a.inApp(append([]string{"composer"}, args...)...))
}

func cmdTest(a *App, args []string) int {
	return a.Run(a.artisan(append([]string{"test"}, args...)...))
}

func cmdTestf(a *App, args []string) int {
	if len(args) == 0 {
		return a.usageError("testf needs a filter, e.g. bothy testf UserTest")
	}
	return a.Run(a.artisan(append([]string{"test", "--filter", args[0]}, args[1:]...)...))
}

func cmdMfs(a *App, args []string) int {
	if len(args) > 0 {
		return a.usageError("mfs takes no arguments")
	}
	return a.runAll(a.artisan("migrate:fresh"), a.artisan("db:seed", "--class="+seeder))
}

func cmdHorizon(a *App, args []string) int {
	return a.Run(a.artisan(append([]string{"horizon"}, args...)...))
}

func cmdNpm(a *App, args []string) int {
	x := append([]string{"run", "--rm"}, a.noTTY()...)
	x = append(x, nodeService, "npm")
	return a.Run(compose(append(x, args...)...))
}

func cmdShell(a *App, args []string) int {
	if len(args) > 0 {
		return a.usageError("shell takes no arguments")
	}
	return a.Run(a.inApp("bash"))
}
