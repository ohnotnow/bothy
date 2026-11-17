package bothy

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"text/tabwriter"
)

// projectSuffix marks a compose project as bothy's: compose.dev.yml names
// its project <app>-dev. Volumes carry only the project name, not the folder
// or compose file, so for volumes this is the only marker there is.
const projectSuffix = "-dev"

type volume struct {
	Name    string
	Project string
	Size    string
	InUse   bool // attached to a container, running or stopped
}

func parseVolumes(out []byte) ([]volume, error) {
	var df struct {
		Volumes []struct {
			Name   string
			Labels string
			Size   string
			Links  string
		}
	}
	if err := json.Unmarshal(out, &df); err != nil {
		return nil, fmt.Errorf("reading docker system df output: %w", err)
	}
	var found []volume
	for _, v := range df.Volumes {
		project := label(v.Labels, "com.docker.compose.project")
		if !strings.HasSuffix(project, projectSuffix) {
			continue
		}
		found = append(found, volume{v.Name, project, v.Size, v.Links != "0"})
	}
	sort.Slice(found, func(i, j int) bool { return found[i].Name < found[j].Name })
	return found, nil
}

// label picks one value out of docker's "k=v,k=v" label string.
func label(labels, key string) string {
	for _, kv := range strings.Split(labels, ",") {
		if k, v, ok := strings.Cut(kv, "="); ok && k == key {
			return v
		}
	}
	return ""
}

func cmdVolumes(a *App, args []string) int {
	if len(args) > 0 {
		return a.usageError("volumes takes no arguments")
	}
	out, err := a.Output([]string{"system", "df", "-v", "--format", "json"})
	if err != nil {
		fmt.Fprintf(a.Stderr, "bothy: asking docker for volumes: %v\n", err)
		return 1
	}
	vs, err := parseVolumes(out)
	if err != nil {
		fmt.Fprintf(a.Stderr, "bothy: %v\n", err)
		return 1
	}
	if len(vs) == 0 {
		fmt.Fprintln(a.Stdout, "No bothy volumes.")
		return 0
	}
	w := tabwriter.NewWriter(a.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "VOLUME\tPROJECT\tSIZE\tIN USE")
	for _, v := range vs {
		inUse := "no"
		if v.InUse {
			inUse = "yes"
		}
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\n", v.Name, v.Project, v.Size, inUse)
	}
	w.Flush()
	fmt.Fprintln(a.Stdout)
	fmt.Fprintln(a.Stdout, "To delete one: bothy destroy from the app's folder, or docker volume rm <volume>.")
	return 0
}
