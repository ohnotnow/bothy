package bothy

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"math"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// watchDir is the folder bothy watch polls, relative to the app's folder.
const watchDir = "resources"

// stamp is what bothy watch compares between polls to spot a changed file.
type stamp struct {
	mod  time.Time
	size int64
}

// cmdWatch runs `npm run build` on the host, then reruns it whenever a file
// under resources/ changes. It runs npm on the Mac, not in the node
// container, because the developer's node_modules holds Mac-only native
// binaries (rolldown, Tailwind's oxide) that a Linux container can't load.
func cmdWatch(a *App, args []string) int {
	fs := flag.NewFlagSet("watch", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	every := fs.Float64("every", 1, "seconds between checks")
	if err := fs.Parse(args); err != nil {
		return a.usageError("watch: %v", err)
	}
	if fs.NArg() > 0 {
		return a.usageError("watch takes no arguments, only --every <seconds>")
	}
	if !(*every > 0) || math.IsInf(*every, 0) {
		return a.usageError("watch: --every needs a number of seconds above 0, e.g. 0.5 or 5")
	}
	interval := time.Duration(*every * float64(time.Second))

	if a.dryRun {
		fmt.Fprintf(a.Stdout, "npm run build  # then again whenever %s/ changes, checked every %gs\n", watchDir, *every)
		return 0
	}
	root := filepath.Join(a.Dir, watchDir)
	if info, err := os.Stat(root); err != nil || !info.IsDir() {
		fmt.Fprintf(a.Stderr, "bothy: no %s folder in %s\n", watchDir, a.Dir)
		return exitNotFound
	}
	if !a.haveNpm("watch") {
		return 1
	}

	// ctrl-c reaches npm too and stops a build; this makes the watch stop
	// with it, rather than carrying on to the next check.
	sigs := make(chan os.Signal, 1)
	signal.Notify(sigs, os.Interrupt)
	defer signal.Stop(sigs)
	wait := func() bool {
		select {
		case <-sigs:
			return false
		case <-time.After(interval):
			return true
		}
	}
	return a.watchLoop(root, wait)
}

// cmdAssets runs `npm run build` on the host once and exits with npm's exit
// code: the one-off build that bothy watch starts with, so a developer or an
// agent can build without leaving a watcher running.
func cmdAssets(a *App, args []string) int {
	if len(args) > 0 {
		return a.usageError("assets takes no arguments")
	}
	if a.dryRun {
		fmt.Fprintln(a.Stdout, "npm run build")
		return 0
	}
	if !a.haveNpm("assets") {
		return 1
	}
	return a.Npm("run", "build")
}

// haveNpm says whether npm is installed, and tells the developer if not.
func (a *App) haveNpm(cmd string) bool {
	if a.Npm == nil {
		fmt.Fprintf(a.Stderr, "bothy: %s runs npm on this machine, and npm isn't on your PATH\n", cmd)
		return false
	}
	return true
}

// watchLoop builds once, then calls wait between checks of root and builds
// again when anything under it changed. It returns when wait returns false.
// A failed build doesn't stop it: the developer fixes the file and saves.
func (a *App) watchLoop(root string, wait func() bool) int {
	a.Npm("run", "build")
	last := snapshot(root)
	for wait() {
		now := snapshot(root)
		changes := changed(last, now)
		if len(changes) == 0 {
			continue
		}
		last = now
		what := changes[0]
		if len(changes) > 1 {
			what += fmt.Sprintf(" and %d more", len(changes)-1)
		}
		fmt.Fprintf(a.Stdout, "\n%s  %s changed, rebuilding\n", a.now().Format("15:04:05"), what)
		a.Npm("run", "build")
	}
	return 0
}

// snapshot stamps every file under root, keyed by its path relative to the
// app's folder (e.g. resources/views/home.blade.php). Dotfiles and dot
// folders are skipped, so an editor's swap files don't trigger builds.
// Files that vanish mid-walk are skipped too.
func snapshot(root string) map[string]stamp {
	out := map[string]stamp{}
	parent := filepath.Dir(root)
	filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if path != root && strings.HasPrefix(d.Name(), ".") {
			if d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if d.IsDir() {
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return nil
		}
		rel, _ := filepath.Rel(parent, path)
		out[rel] = stamp{info.ModTime(), info.Size()}
		return nil
	})
	return out
}

// changed lists, sorted, the files added, removed or modified between two
// snapshots.
func changed(before, after map[string]stamp) []string {
	var out []string
	for p, s := range after {
		if b, ok := before[p]; !ok || b != s {
			out = append(out, p)
		}
	}
	for p := range before {
		if _, ok := after[p]; !ok {
			out = append(out, p)
		}
	}
	sort.Strings(out)
	return out
}

// findNpm returns a function that runs npm in dir attached to the terminal,
// or nil if npm isn't on PATH.
func findNpm(dir string) func(args ...string) int {
	path, err := exec.LookPath("npm")
	if err != nil {
		return nil
	}
	return func(args ...string) int {
		cmd := exec.Command(path, args...)
		cmd.Dir = dir
		cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
		err := cmd.Run()
		var exitErr *exec.ExitError
		switch {
		case err == nil:
			return 0
		case errors.As(err, &exitErr):
			return exitErr.ExitCode()
		default:
			fmt.Fprintf(os.Stderr, "bothy: running npm: %v\n", err)
			return 127
		}
	}
}
