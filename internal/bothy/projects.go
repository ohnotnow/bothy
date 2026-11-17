package bothy

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
	"text/tabwriter"
)

// project is one running bothy stack, as Docker reports it.
type project struct {
	Name   string
	Status string
	File   string // absolute path to its compose.dev.yml
}

// runningProjects asks Docker for running compose projects and keeps those
// started from a compose.dev.yml. Docker records this on the containers, so
// bothy keeps no list of its own.
func (a *App) runningProjects() ([]project, error) {
	out, err := a.Output([]string{"compose", "ls", "--format", "json"})
	if err != nil {
		return nil, fmt.Errorf("asking docker for running projects: %w", err)
	}
	return parseProjects(out)
}

func parseProjects(out []byte) ([]project, error) {
	var rows []struct {
		Name        string
		Status      string
		ConfigFiles string
	}
	if err := json.Unmarshal(out, &rows); err != nil {
		return nil, fmt.Errorf("reading docker compose ls output: %w", err)
	}
	var found []project
	for _, r := range rows {
		for _, f := range strings.Split(r.ConfigFiles, ",") {
			if filepath.Base(f) == ComposeFile {
				found = append(found, project{Name: r.Name, Status: r.Status, File: f})
				break
			}
		}
	}
	return found, nil
}

func cmdLs(a *App, args []string) int {
	if len(args) > 0 {
		return a.usageError("ls takes no arguments")
	}
	ps, err := a.runningProjects()
	if err != nil {
		fmt.Fprintf(a.Stderr, "bothy: %v\n", err)
		return 1
	}
	if len(ps) == 0 {
		fmt.Fprintln(a.Stdout, "No bothy projects running.")
		return 0
	}
	w := tabwriter.NewWriter(a.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "NAME\tSTATUS\tFOLDER")
	for _, p := range ps {
		fmt.Fprintf(w, "%s\t%s\t%s\n", p.Name, p.Status, filepath.Dir(p.File))
	}
	w.Flush()
	return 0
}

func cmdPoweroff(a *App, args []string) int {
	if len(args) > 0 {
		return a.usageError("poweroff takes no arguments")
	}
	ps, err := a.runningProjects()
	if err != nil {
		fmt.Fprintf(a.Stderr, "bothy: %v\n", err)
		return 1
	}
	if len(ps) == 0 {
		fmt.Fprintln(a.Stdout, "No bothy projects running.")
		return 0
	}
	failed := 0
	for _, p := range ps {
		fmt.Fprintf(a.Stdout, "Stopping %s (%s)\n", p.Name, filepath.Dir(p.File))
		if a.Run([]string{"compose", "-f", p.File, "down"}) != 0 {
			fmt.Fprintf(a.Stderr, "bothy: could not stop %s\n", p.Name)
			failed++
		}
	}
	if failed > 0 {
		return 1
	}
	return 0
}
