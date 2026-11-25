package bothy

import (
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"strings"
	"testing"
	"time"
)

// Dependabot rewrites this line in proxy-compose.yml, and a tag with no
// digest could be swapped for another image under the same name.
func TestProxyImageIsPinnedByVersionAndDigest(t *testing.T) {
	pinned := regexp.MustCompile(`(?m)^    image: traefik:v\d+\.\d+\.\d+@sha256:[0-9a-f]{64}$`)
	if !pinned.MatchString(proxyCompose) {
		t.Fatalf("the traefik image isn't pinned as traefik:vX.Y.Z@sha256:<digest>:\n%s", proxyCompose)
	}
}

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

func TestStartWithoutMkcertExplainsAndStops(t *testing.T) {
	a, calls, _, stderr := fakeApp(t, true)
	os.RemoveAll(a.certDir())
	a.Mkcert = nil
	if code := a.Main([]string{"start"}); code != 1 {
		t.Fatalf("exit %d, want 1", code)
	}
	if len(*calls) != 0 {
		t.Fatalf("docker was called: %q", *calls)
	}
	got := stderr.String()
	for _, s := range []string{"brew install mkcert nss", "mkcert -install", "asks for your password", "Restart Firefox"} {
		if !strings.Contains(got, s) {
			t.Errorf("message missing %q:\n%s", s, got)
		}
	}
}

func TestStartMakesTheCertificateOnce(t *testing.T) {
	a, _, _, _ := fakeApp(t, true)
	os.RemoveAll(a.certDir())
	var made [][]string
	a.Mkcert = func(args ...string) error {
		made = append(made, args)
		return fakeMkcert(args...)
	}
	a.Main([]string{"start"})
	a.Main([]string{"start"})
	if len(made) != 1 {
		t.Fatalf("mkcert ran %d times, want 1", len(made))
	}
	want := []string{"-cert-file", filepath.Join(a.certDir(), certFile), "-key-file", filepath.Join(a.certDir(), keyFile), "*.bothy.localhost"}
	if !reflect.DeepEqual(made[0], want) {
		t.Fatalf("mkcert %q, want %q", made[0], want)
	}
}

func TestStartUpdatesARunningProxyWhenItsSettingsChanged(t *testing.T) {
	a, calls, _, _ := fakeApp(t, true)
	// as if the proxy was started by an older bothy
	os.WriteFile(a.proxyFile(), []byte("old settings\n"), 0o644)
	a.Output = func(args []string) ([]byte, error) {
		if strings.HasPrefix(strings.Join(args, " "), "ps --filter") {
			t.Fatal("checked ports for a proxy that already holds them")
		}
		return fakeOutput(nil)(args)
	}
	a.Main([]string{"start"})
	want := [][]string{
		{"compose", "-f", a.proxyFile(), "up", "-d", "--force-recreate"},
		words("compose -f compose.dev.yml up -d --build"),
	}
	if !reflect.DeepEqual(*calls, want) {
		t.Fatalf("got %q, want %q", *calls, want)
	}
}

func TestCertificateRenewal(t *testing.T) {
	for _, tc := range []struct {
		name      string
		daysLeft  int
		mkcert    bool
		wantCode  int
		wantMade  bool
		wantNote  string
		wantCalls int // docker calls: proxy restart (if made) + app up
	}{
		{"plenty left", 200, true, 0, false, "", 1},
		{"31 days left", 31, true, 0, false, "", 1},
		{"29 days left renews", 29, true, 0, true, "Renewing the bothy certificate, which had 28 days left", 2},
		{"expired renews", -5, true, 0, true, "Renewing the bothy certificate, which had 0 days left", 2},
		{"near expiry without mkcert warns", 10, false, 0, false, "expires in 9 days, and mkcert isn't installed", 1},
		{"expired without mkcert stops", -1, false, 1, false, "mkcert isn't installed", 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			a, calls, stdout, stderr := fakeApp(t, true)
			// replace the settled certificate with one that has daysLeft left
			args := []string{"-cert-file", filepath.Join(a.certDir(), certFile), "-key-file", filepath.Join(a.certDir(), keyFile)}
			if err := writeCert(args, time.Now().Add(time.Duration(tc.daysLeft)*24*time.Hour)); err != nil {
				t.Fatal(err)
			}
			made := false
			a.Mkcert = nil
			if tc.mkcert {
				a.Mkcert = func(args ...string) error { made = true; return fakeMkcert(args...) }
			}
			if code := a.Main([]string{"start"}); code != tc.wantCode {
				t.Fatalf("exit %d, want %d; stderr %q", code, tc.wantCode, stderr)
			}
			if made != tc.wantMade {
				t.Fatalf("made a certificate: %v, want %v", made, tc.wantMade)
			}
			if out := stdout.String() + stderr.String(); !strings.Contains(out, tc.wantNote) {
				t.Fatalf("output missing %q:\n%s", tc.wantNote, out)
			}
			if len(*calls) != tc.wantCalls {
				t.Fatalf("docker calls %q, want %d", *calls, tc.wantCalls)
			}
			if tc.wantMade && !reflect.DeepEqual((*calls)[0], []string{"compose", "-f", a.proxyFile(), "up", "-d", "--force-recreate"}) {
				t.Fatalf("proxy not recreated: %q", (*calls)[0])
			}
		})
	}
}
