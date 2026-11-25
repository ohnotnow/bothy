package bothy

import (
	"bytes"
	_ "embed"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
)

// The shared Traefik proxy that serves every app at
// https://<app>.bothy.localhost. Apps opt in with traefik labels and by
// joining the "bothy" network. HTTPS and the redirect from HTTP are set on
// the proxy's entrypoints, so apps' labels don't mention TLS at all.
const proxyProject = "bothy-proxy"

// proxyCompose is the proxy's compose file, which bothy writes to its config
// folder on every start. It lives in a file of its own, proxy-compose.yml, so
// that Dependabot can see the Traefik image pinned in it and offer updates
// (.github/dependabot.yml). Dependabot can't read an image out of a Go string.
//
//go:embed proxy-compose.yml
var proxyCompose string

const (
	// The mkcert certificate is the default for every HTTPS request.
	proxyDynamic = `# Written by bothy on every start; edits here are overwritten.
tls:
  stores:
    default:
      defaultCertificate:
        certFile: /certs/` + certFile + `
        keyFile: /certs/` + keyFile + `
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

// writeIfChanged writes content to path and reports whether it differed
// from what was there.
func writeIfChanged(path, content string) (bool, error) {
	old, err := os.ReadFile(path)
	if err == nil && bytes.Equal(old, []byte(content)) {
		return false, nil
	}
	return true, os.WriteFile(path, []byte(content), 0o644)
}

// writeProxyFiles writes the proxy's compose and traefik files, and reports
// whether either changed, which means a running proxy needs recreating.
func (a *App) writeProxyFiles() (bool, error) {
	if err := os.MkdirAll(a.ConfigDir, 0o755); err != nil {
		return false, err
	}
	c1, err := writeIfChanged(a.proxyFile(), proxyCompose)
	if err != nil {
		return false, err
	}
	c2, err := writeIfChanged(filepath.Join(a.ConfigDir, "traefik-dynamic.yml"), proxyDynamic)
	return c1 || c2, err
}

// ensureProxy makes sure the shared proxy is running with bothy's current
// settings and certificate.
func (a *App) ensureProxy() int {
	up := []string{"compose", "-f", a.proxyFile(), "up", "-d"}
	if a.dryRun {
		fmt.Fprintln(a.Stdout, "# only if the bothy proxy is not already running with these settings:")
		return a.Run(up)
	}
	code, newCert := a.ensureCert()
	if code != 0 {
		return code
	}
	changed, err := a.writeProxyFiles()
	if err != nil {
		fmt.Fprintf(a.Stderr, "bothy: writing the proxy's settings: %v\n", err)
		return 1
	}
	running, err := a.proxyRunning()
	if err != nil {
		fmt.Fprintf(a.Stderr, "bothy: %v\n", err)
		return 1
	}
	switch {
	case running && !changed && !newCert:
		return 0
	case running:
		// It holds ports 80 and 443 itself, so skip the port check.
		// --force-recreate because a new certificate alone doesn't change
		// the compose file, and traefik only reads it at start.
		fmt.Fprintln(a.Stdout, "Restarting the bothy proxy with its new settings")
		return a.Run(append(up, "--force-recreate"))
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
