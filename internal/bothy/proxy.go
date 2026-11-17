package bothy

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
)

// The shared Traefik proxy that serves every app at <app>.bothy.localhost.
// Apps opt in with traefik labels and by joining the "bothy" network.
const (
	proxyProject = "bothy-proxy"
	proxyNetwork = "bothy"
	proxyCompose = `# Written by bothy on every start; edits here are overwritten.
name: bothy-proxy
services:
  traefik:
    image: traefik:v3.7.18@sha256:9c437ac4f09ce6f4980a8544902d3475fdf70a1a7c578729581d2535dfa7d635
    command:
      - --providers.docker=true
      - --providers.docker.exposedbydefault=false
      - --providers.docker.network=bothy
      - --entrypoints.web.address=:80
      - --entrypoints.websecure.address=:443
    ports:
      - "127.0.0.1:80:80"
      - "127.0.0.1:443:443"
    volumes:
      - /var/run/docker.sock:/var/run/docker.sock:ro
    networks:
      - bothy
networks:
  bothy:
    name: bothy
`
)

const lando = "If it's lando's proxy, run 'lando poweroff' first."

// portHolders names the containers publishing port 80 or 443. bothy asks
// Docker rather than test-binding the ports itself, because macOS refuses an
// ordinary user a port below 1024 on 127.0.0.1 even when it is free.
func (a *App) portHolders() ([]string, error) {
	var names []string
	for _, port := range []string{"80", "443"} {
		out, err := a.Output([]string{"ps", "--filter", "publish=" + port, "--format", "{{.Names}}"})
		if err != nil {
			return nil, fmt.Errorf("asking docker which containers use port %s: %w", port, err)
		}
		for _, n := range strings.Fields(string(out)) {
			if !slices.Contains(names, n) {
				names = append(names, n)
			}
		}
	}
	return names, nil
}

func (a *App) proxyFile() string {
	return filepath.Join(a.ConfigDir, "proxy-compose.yml")
}

// proxyRunning asks Docker whether the bothy-proxy project is up.
func (a *App) proxyRunning() (bool, error) {
	out, err := a.Output([]string{"compose", "ls", "--format", "json"})
	if err != nil {
		return false, fmt.Errorf("asking docker for running projects: %w", err)
	}
	var rows []struct{ Name, Status string }
	if err := json.Unmarshal(out, &rows); err != nil {
		return false, fmt.Errorf("reading docker compose ls output: %w", err)
	}
	for _, r := range rows {
		if r.Name == proxyProject && strings.HasPrefix(r.Status, "running") {
			return true, nil
		}
	}
	return false, nil
}

// ensureProxy starts the shared proxy unless it is already running.
func (a *App) ensureProxy() int {
	up := []string{"compose", "-f", a.proxyFile(), "up", "-d"}
	if a.dryRun {
		fmt.Fprintln(a.Stdout, "# only if the bothy proxy is not already running:")
		return a.Run(up)
	}
	running, err := a.proxyRunning()
	if err != nil {
		fmt.Fprintf(a.Stderr, "bothy: %v\n", err)
		return 1
	}
	if running {
		return 0
	}
	holders, err := a.portHolders()
	if err != nil {
		fmt.Fprintf(a.Stderr, "bothy: %v\n", err)
		return 1
	}
	if len(holders) > 0 {
		fmt.Fprintf(a.Stderr, "bothy: port 80 or 443, which the bothy proxy needs, is in use by: %s\n", strings.Join(holders, ", "))
		fmt.Fprintln(a.Stderr, lando)
		return 1
	}
	if err := os.MkdirAll(a.ConfigDir, 0o755); err != nil {
		fmt.Fprintf(a.Stderr, "bothy: %v\n", err)
		return 1
	}
	if err := os.WriteFile(a.proxyFile(), []byte(proxyCompose), 0o644); err != nil {
		fmt.Fprintf(a.Stderr, "bothy: %v\n", err)
		return 1
	}
	fmt.Fprintln(a.Stdout, "Starting the bothy proxy")
	if code := a.Run(up); code != 0 {
		fmt.Fprintln(a.Stderr, "bothy: the proxy did not start. Something outside Docker may be using port 80 or 443.")
		return code
	}
	return 0
}

// stopProxy stops the shared proxy if it is running.
func (a *App) stopProxy() int {
	running, err := a.proxyRunning()
	if err != nil {
		fmt.Fprintf(a.Stderr, "bothy: %v\n", err)
		return 1
	}
	if !running {
		return 0
	}
	fmt.Fprintln(a.Stdout, "Stopping the bothy proxy")
	return a.Run([]string{"compose", "-f", a.proxyFile(), "down"})
}
