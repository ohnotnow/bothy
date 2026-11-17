package bothy

import (
	"os"
	"reflect"
	"strings"
	"testing"
)

// noProxy answers `docker compose ls` with nothing running and every other
// captured command with no output.
func noProxy(args []string) ([]byte, error) {
	if strings.HasPrefix(strings.Join(args, " "), "compose ls") {
		return []byte("[]"), nil
	}
	return nil, nil
}

func TestStartStartsTheProxyWhenItIsNotRunning(t *testing.T) {
	a, calls, _, _ := fakeApp(t, true)
	a.Output = noProxy
	if code := a.Main([]string{"start"}); code != 0 {
		t.Fatalf("exit %d", code)
	}
	want := [][]string{
		{"compose", "-f", a.proxyFile(), "up", "-d"},
		words("compose -f compose.dev.yml up -d --build"),
	}
	if !reflect.DeepEqual((*calls)[:2], want) {
		t.Fatalf("got %q, want %q", *calls, want)
	}
	written, err := os.ReadFile(a.proxyFile())
	if err != nil || string(written) != proxyCompose {
		t.Fatalf("proxy compose file not written: %v", err)
	}
}

func TestStartLeavesARunningProxyAlone(t *testing.T) {
	a, calls, _, _ := fakeApp(t, true)
	a.Main([]string{"start"})
	if len(*calls) != 1 {
		t.Fatalf("got %q, want only the app's up", *calls)
	}
}

func TestStartBailsWhenProxyPortsAreTaken(t *testing.T) {
	a, calls, _, stderr := fakeApp(t, true)
	a.Output = func(args []string) ([]byte, error) {
		switch strings.Join(args, " ") {
		case "ps --filter publish=80 --format {{.Names}}", "ps --filter publish=443 --format {{.Names}}":
			return []byte("landoproxy_proxy_1\n"), nil
		}
		return noProxy(args)
	}
	if code := a.Main([]string{"start"}); code != 1 {
		t.Fatalf("exit %d, want 1", code)
	}
	if len(*calls) != 0 {
		t.Fatalf("docker was called: %q", *calls)
	}
	got := stderr.String()
	if !strings.Contains(got, "in use by: landoproxy_proxy_1\n") || !strings.Contains(got, "lando poweroff") {
		t.Fatalf("stderr %q", got)
	}
}

func TestPoweroffStopsTheProxyToo(t *testing.T) {
	a, calls, _, _ := fakeApp(t, false)
	if code := a.Main([]string{"poweroff"}); code != 0 {
		t.Fatalf("exit %d", code)
	}
	want := [][]string{{"compose", "-f", a.proxyFile(), "down"}}
	if !reflect.DeepEqual(*calls, want) {
		t.Fatalf("got %q, want %q", *calls, want)
	}
}
