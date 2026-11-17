// Package bothy turns short commands into docker compose calls for the
// team's Laravel apps.
package bothy

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"
)

// ComposeFile is the dev compose file bothy expects in the current folder.
const ComposeFile = "compose.dev.yml"

// Version is overwritten by -ldflags at release.
var Version = "dev"

const (
	exitUsage    = 64
	exitNotFound = 66
)

// App holds everything a command needs, so tests can swap out docker.
type App struct {
	Dir         string
	Interactive bool // stdin and stdout are a terminal
	Stdin       io.Reader
	Stdout      io.Writer
	Stderr      io.Writer
	// Run runs docker with the given arguments attached to the terminal and
	// returns its exit code.
	Run func(args []string) int
	// Output runs docker and returns what it printed on stdout.
	Output func(args []string) ([]byte, error)
	// Pipe runs docker with stdin and stdout redirected and returns its
	// exit code. Used by the database import and export.
	Pipe func(args []string, stdin io.Reader, stdout io.Writer) int

	dryRun bool
}

type command struct {
	name     string
	usage    string
	summary  string
	needFile bool
	run      func(a *App, args []string) int
}

var commands []command

func init() {
	commands = []command{
		{"start", "", "start the app's stack", true, cmdStart},
		{"stop", "", "stop the app's stack", true, cmdStop},
		{"info", "", "show the app's addresses and ports", true, cmdInfo},
		{"rebuild", "", "stop, rebuild with fresh base images, start (keeps the data)", true, cmdRebuild},
		{"destroy", "", "stop and delete the app's volumes, including the database (asks first)", true, cmdDestroy},
		{"artisan", "<args>", "run php artisan in the app container", true, cmdArtisan},
		{"tinker", "", "open tinker", true, cmdTinker},
		{"composer", "<args>", "run composer in the app container", true, cmdComposer},
		{"test", "[args]", "run the tests", true, cmdTest},
		{"testf", "<filter> [args]", "run the tests matching a filter", true, cmdTestf},
		{"mfs", "", "migrate:fresh, then seed with TestDataSeeder", true, cmdMfs},
		{"horizon", "", "run horizon in the foreground (ctrl-c to stop)", true, cmdHorizon},
		{"npm", "<args>", "run npm in the node container", true, cmdNpm},
		{"shell", "", "open a shell in the app container", true, cmdShell},
		{"ssh", "", "same as shell", true, cmdShell},
		{"mysql", "", "open a mariadb client on the app's database, as root", true, cmdMysql},
		{"database", "", "same as mysql", true, cmdMysql},
		{"db-import", "<file>", "wipe the database and load a .sql or .sql.gz dump", true, cmdDbImport},
		{"db-export", "[file]", "dump the database (default <folder>-<time>.sql.gz)", true, cmdDbExport},
		{"ls", "", "list running bothy projects", false, cmdLs},
		{"poweroff", "", "stop every running bothy project", false, cmdPoweroff},
		{"volumes", "", "list every bothy volume and its size, running or not", false, cmdVolumes},
		{"version", "", "print the version", false, cmdVersion},
		{"help", "", "show this help", false, cmdHelp},
	}
}

// Main is the real entry point: it wires App to docker and the terminal.
func Main(args []string) int {
	dir, err := os.Getwd()
	if err != nil {
		fmt.Fprintf(os.Stderr, "bothy: %v\n", err)
		return 1
	}
	a := &App{
		Dir:         dir,
		Interactive: isTerminal(os.Stdin) && isTerminal(os.Stdout),
		Stdin:       os.Stdin,
		Stdout:      os.Stdout,
		Stderr:      os.Stderr,
		Run:         runDocker,
		Output:      outputDocker,
		Pipe:        pipeDocker,
	}
	return a.Main(args)
}

// Main parses the global flags and dispatches to a command.
func (a *App) Main(args []string) int {
	fs := flag.NewFlagSet("bothy", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	dryRun := fs.Bool("dry-run", false, "print the docker commands instead of running them")
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			a.printHelp()
			return 0
		}
		fmt.Fprintf(a.Stderr, "bothy: %v\n", err)
		return exitUsage
	}
	if *dryRun {
		a.dryRun = true
		a.Run = func(args []string) int {
			fmt.Fprintln(a.Stdout, shellJoin(append([]string{"docker"}, args...)))
			return 0
		}
	}

	rest := fs.Args()
	if len(rest) == 0 {
		a.printHelp()
		return exitUsage
	}
	name, cmdArgs := rest[0], rest[1:]
	for _, c := range commands {
		if c.name != name {
			continue
		}
		if c.needFile && !a.hasComposeFile() {
			fmt.Fprintf(a.Stderr, "bothy: no %s in %s\n", ComposeFile, a.Dir)
			return exitNotFound
		}
		return c.run(a, cmdArgs)
	}
	fmt.Fprintf(a.Stderr, "bothy: unknown command %q (run 'bothy help')\n", name)
	return exitUsage
}

func (a *App) hasComposeFile() bool {
	info, err := os.Stat(a.Dir + string(os.PathSeparator) + ComposeFile)
	return err == nil && !info.IsDir()
}

func (a *App) usageError(format string, args ...any) int {
	fmt.Fprintf(a.Stderr, "bothy: "+format+"\n", args...)
	return exitUsage
}

func (a *App) printHelp() {
	fmt.Fprintf(a.Stdout, "bothy runs docker compose for the app in the current folder (it needs %s there).\n\n", ComposeFile)
	fmt.Fprintln(a.Stdout, "Usage: bothy [--dry-run] <command> [args]")
	fmt.Fprintln(a.Stdout)
	width := 0
	for _, c := range commands {
		width = max(width, len(strings.TrimSpace(c.name+" "+c.usage)))
	}
	for _, c := range commands {
		fmt.Fprintf(a.Stdout, "  %-*s  %s\n", width, strings.TrimSpace(c.name+" "+c.usage), c.summary)
	}
	fmt.Fprintln(a.Stdout)
	fmt.Fprintln(a.Stdout, "--dry-run prints the docker commands instead of running them.")
}

func cmdHelp(a *App, _ []string) int {
	a.printHelp()
	return 0
}

func cmdVersion(a *App, _ []string) int {
	fmt.Fprintf(a.Stdout, "bothy %s\n", Version)
	return 0
}

// shellJoin renders args as a command line a person could paste.
func shellJoin(args []string) string {
	out := make([]string, len(args))
	for i, s := range args {
		if s == "" || strings.ContainsAny(s, " \t\n'\"\\$`*?[]{}()<>|&;#~") {
			s = "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
		}
		out[i] = s
	}
	return strings.Join(out, " ")
}
