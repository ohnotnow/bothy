package bothy

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
	"text/tabwriter"
)

// Ports that are not web pages, so info prints host:port instead of a URL.
var plainPorts = map[int]bool{3306: true, 6379: true, 1025: true}

type published struct {
	Service string
	Host    string
	Port    int
	Target  int
}

// parsePublished reads `docker compose ps --format json`, which prints one
// JSON object per line, and returns each port published on the host.
func parsePublished(out []byte) ([]published, error) {
	type row struct {
		Service    string
		Publishers []struct {
			URL           string
			TargetPort    int
			PublishedPort int
		}
	}
	var found []published
	seen := map[string]bool{}
	sc := bufio.NewScanner(bytes.NewReader(out))
	sc.Buffer(make([]byte, 0, 64*1024), 4*1024*1024) // label lists make long lines
	for sc.Scan() {
		line := bytes.TrimSpace(sc.Bytes())
		if len(line) == 0 {
			continue
		}
		var r row
		if err := json.Unmarshal(line, &r); err != nil {
			return nil, fmt.Errorf("reading docker compose ps output: %w", err)
		}
		for _, p := range r.Publishers {
			if p.PublishedPort == 0 {
				continue // exposed inside Docker only
			}
			key := fmt.Sprintf("%s/%d/%d", r.Service, p.TargetPort, p.PublishedPort)
			if seen[key] {
				continue // the same port on IPv4 and IPv6
			}
			seen[key] = true
			found = append(found, published{r.Service, p.URL, p.PublishedPort, p.TargetPort})
		}
	}
	return found, sc.Err()
}

// address renders a published port the way a person would use it.
func (p published) address() string {
	host := p.Host
	if host == "" || host == "0.0.0.0" || host == "::" {
		host = "127.0.0.1"
	}
	if plainPorts[p.Target] {
		return fmt.Sprintf("%s:%d", host, p.Port)
	}
	return fmt.Sprintf("http://%s:%d", host, p.Port)
}

// dbDetails asks the database container for the login a GUI client needs.
// It is a local development password, so printing it is fine. Returns "" if
// it can't get all three.
func (a *App) dbDetails() string {
	script := `printf '%s\n%s\n%s\n' "$MARIADB_DATABASE" "$MARIADB_USER" "$MARIADB_PASSWORD"`
	out, err := a.Output(compose("exec", "-T", dbService, "sh", "-c", script))
	if err != nil {
		return ""
	}
	f := strings.Split(strings.TrimSuffix(string(out), "\n"), "\n")
	if len(f) != 3 || f[0] == "" || f[1] == "" {
		return ""
	}
	return fmt.Sprintf("database %s, user %s, password %s", f[0], f[1], f[2])
}

func cmdInfo(a *App, args []string) int {
	if len(args) > 0 {
		return a.usageError("info takes no arguments")
	}
	return a.info()
}

func (a *App) info() int {
	out, err := a.Output(compose("ps", "--format", "json"))
	if err != nil {
		fmt.Fprintf(a.Stderr, "bothy: asking docker for the app's ports: %v\n", err)
		return 1
	}
	ps, err := parsePublished(out)
	if err != nil {
		fmt.Fprintf(a.Stderr, "bothy: %v\n", err)
		return 1
	}
	if len(ps) == 0 {
		fmt.Fprintf(a.Stdout, "%s has nothing running with a port (bothy start starts it).\n", filepath.Base(a.Dir))
		return 0
	}
	w := tabwriter.NewWriter(a.Stdout, 0, 0, 2, ' ', 0)
	for _, p := range ps {
		extra := ""
		if p.Service == dbService {
			if d := a.dbDetails(); d != "" {
				extra = "  (" + d + ")"
			}
		}
		fmt.Fprintf(w, "%s\t%s%s\n", p.Service, p.address(), extra)
	}
	w.Flush()
	return 0
}
