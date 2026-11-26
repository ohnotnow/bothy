// Package bothy turns short commands into docker compose calls for the
// team's Laravel apps.
package bothy

import (
	"bufio"
	"errors"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"
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
	// ConfigDir holds the files bothy writes for itself (the proxy's
	// compose file).
	ConfigDir string
	// BackupDir is where db-import backs up the database before wiping it.
	// The system temp folder, so the OS clears old backups away.
	BackupDir string
	// Mkcert runs mkcert; nil when it isn't installed.
	Mkcert func(args ...string) error
	// Npm runs npm on this machine (not in a container) and returns its
	// exit code; nil when it isn't installed. Used by assets and watch.
	Npm func(args ...string) int
	// Git runs git in the app folder, saying nothing, and returns its exit
	// code; nil when it isn't installed. Used to check a dump is ignored.
	Git func(args ...string) int
	// LanAddress returns this machine's address on the local network, which
	// info shows for ports open to the network; nil or "" means 127.0.0.1.
	LanAddress func() string
	// Now is the clock; nil means time.Now.
	Now func() time.Time
	// ReleaseURL is the GitHub API address of the latest release; empty
	// skips the version check.
	ReleaseURL string
	// Exe is the path of the running binary, which self-update replaces.
	Exe string
	// TryBinary runs a freshly installed binary once to check it works.
	TryBinary func(path string) error
	// Skills is the built-in claude/ folder; nil when there is none.
	Skills fs.FS
	// ClaudeDir is where install-skills puts the skills (~/.claude).
	ClaudeDir string

	dryRun bool
	// bind is what start or rebuild set BOTHY_BIND to; "" for other commands.
	bind string
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
		{"start", "[--lan]", "start the app's stack (--lan opens it to the network)", true, cmdStart},
		{"stop", "", "stop the app's stack", true, cmdStop},
		{"info", "", "show the app's addresses and ports", true, cmdInfo},
		{"logs", "[args]", "show the stack's logs (docker compose logs), e.g. bothy logs -f appserver", true, cmdLogs},
		{"rebuild", "[--lan]", "stop, rebuild with fresh base images, start (keeps the data)", true, cmdRebuild},
		{"destroy", "", "stop and delete the app's volumes, including the database (asks first)", true, cmdDestroy},
		{"artisan", "<args>", "run php artisan in the app container", true, cmdArtisan},
		{"tinker", "", "open tinker", true, cmdTinker},
		{"composer", "<args>", "run composer in the app container", true, cmdComposer},
		{"test", "[args]", "run the tests", true, cmdTest},
		{"testf", "<filter> [args]", "run the tests matching a filter", true, cmdTestf},
		{"pest", "[args]", "run vendor/bin/pest in the app container", true, cmdPest},
		{"lint", "[args]", "run pint in the app container (fixes files; --test only checks)", true, cmdLint},
		{"pint", "[args]", "same as lint", true, cmdLint},
		{"ci", "", "run the team's usual CI checks: env keys, stray dd/dump, composer audit, pint, tests on MariaDB", true, cmdCi},
		{"mfs", "[args]", "wipe the database: migrate:fresh, seeding with TestDataSeeder (or --seeder=X)", true, cmdMfs},
		{"horizon", "", "run horizon in the foreground (ctrl-c to stop)", true, cmdHorizon},
		{"assets", "", "npm run build on this machine, once", true, cmdAssets},
		{"watch", "[--every N]", "npm run build on this machine, again whenever resources/ changes", true, cmdWatch},
		{"shell", "", "open a shell in the app container", true, cmdShell},
		{"ssh", "", "same as shell", true, cmdShell},
		{"mysql", "", "open a mariadb client on the app's database, as root", true, cmdMysql},
		{"database", "", "same as mysql", true, cmdMysql},
		{"db-import", "<file>", "back up and wipe the database, then load a .sql or .sql.gz dump", true, cmdDbImport},
		{"db-export", "[file]", "dump the database (default <folder>-<time>.sql.gz)", true, cmdDbExport},
		{"ls", "", "list running bothy projects", false, cmdLs},
		{"poweroff", "", "stop every running bothy project", false, cmdPoweroff},
		{"volumes", "", "list every bothy volume and its size, running or not", false, cmdVolumes},
		{"version", "", "print the version and say if a newer release is out", false, cmdVersion},
		{"self-update", "[--check] [--yes]", "replace bothy with the latest release", false, cmdSelfUpdate},
		{"install-skills", "[--all] [--dry-run] [--force] [--dir D]", "install the Claude Code skill for this bothy (--all adds the lando migration skill)", false, cmdInstallSkills},
		{"help", "", "show this help", false, cmdHelp},
	}
}

// Main is the real entry point: it wires App to docker and the terminal.
// skills is the built-in claude/ folder.
func Main(args []string, skills fs.FS) int {
	dir, err := os.Getwd()
	if err != nil {
		fmt.Fprintf(os.Stderr, "bothy: %v\n", err)
		return 1
	}
	config, err := os.UserConfigDir()
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
		ConfigDir:   filepath.Join(config, "bothy"),
		BackupDir:   os.TempDir(),
		Mkcert:      findMkcert(),
		Npm:         findNpm(dir),
		Git:         findGit(dir),
		LanAddress:  findLanAddress,
		ReleaseURL:  LatestReleaseAPI,
		Exe:         executable(),
		TryBinary:   tryBinary,
		Skills:      skills,
		ClaudeDir:   DefaultClaudeDir(),
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
			line := append([]string{"docker"}, args...)
			if a.bind != "" {
				line = append([]string{bindEnv + "=" + a.bind}, line...)
			}
			fmt.Fprintln(a.Stdout, shellJoin(line))
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

// askYes prints question with a [y/N] prompt and reports whether the answer
// was y or yes. Anything else, including no answer, is no.
func (a *App) askYes(question string) bool {
	fmt.Fprint(a.Stdout, question+" [y/N] ")
	answer, _ := bufio.NewReader(a.Stdin).ReadString('\n')
	ans := strings.ToLower(strings.TrimSpace(answer))
	return ans == "y" || ans == "yes"
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

// executable is the real path of the running binary, symlinks resolved, or
// "" if it can't be found.
func executable() string {
	exe, err := os.Executable()
	if err != nil {
		return ""
	}
	if real, err := filepath.EvalSymlinks(exe); err == nil {
		return real
	}
	return exe
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
