package bothy

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"
)

func TestIsNewer(t *testing.T) {
	cases := []struct {
		latest, current string
		want            bool
	}{
		{"v0.2.0", "v0.1.0", true},
		{"v0.10.0", "v0.9.0", true},
		{"v1.0.0", "v0.99.99", true},
		{"v0.1.1", "v0.1.0", true},
		{"v0.1.0", "v0.1.0", false},
		{"v0.1.0", "v0.2.0", false},
		// source builds compare on the release they're based on
		{"v0.2.0", "v0.1.0-5-gabc1234", true},
		{"v0.1.0", "v0.1.0-5-gabc1234", false},
		{"v0.1.0", "v0.1.0-dirty", false},
		// unreadable versions are never newer
		{"v0.2.0", "abc1234", false},
		{"v0.2.0", "dev", false},
		{"latest", "v0.1.0", false},
	}
	for _, c := range cases {
		if got := isNewer(c.latest, c.current); got != c.want {
			t.Errorf("isNewer(%q, %q) = %v, want %v", c.latest, c.current, got, c.want)
		}
	}
}

func TestLookupChecksum(t *testing.T) {
	sums := "aaa  bothy-darwin-arm64\nbbb *bothy-linux-amd64\n\n"
	if got, ok := lookupChecksum(sums, "bothy-darwin-arm64"); !ok || got != "aaa" {
		t.Errorf("text mode line: got %q, %v", got, ok)
	}
	if got, ok := lookupChecksum(sums, "bothy-linux-amd64"); !ok || got != "bbb" {
		t.Errorf("binary mode line: got %q, %v", got, ok)
	}
	if _, ok := lookupChecksum(sums, "bothy-linux-arm64"); ok {
		t.Error("found a line for an asset that isn't listed")
	}
}

// setBuild sets the stamped version variables for one test.
func setBuild(t *testing.T, version, fromSource, built string) {
	t.Helper()
	oldV, oldS, oldB := Version, FromSource, Built
	Version, FromSource, Built = version, fromSource, built
	t.Cleanup(func() { Version, FromSource, Built = oldV, oldS, oldB })
}

// fakeRelease serves a GitHub-style latest release with this platform's
// binary and a SHA256SUMS file. Change its fields before the first request
// to break it in particular ways.
type fakeRelease struct {
	tag      string
	binary   []byte
	sums     string // "" means work them out from binary
	noSums   bool
	down     bool // answer every request with a 500
	requests atomic.Int32
	server   *httptest.Server
}

func newFakeRelease(t *testing.T, tag string) *fakeRelease {
	t.Helper()
	f := &fakeRelease{tag: tag, binary: []byte("new bothy " + tag)}
	asset := assetName(runtime.GOOS, runtime.GOARCH)
	f.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.requests.Add(1)
		if f.down {
			http.Error(w, "down", http.StatusInternalServerError)
			return
		}
		switch r.URL.Path {
		case "/latest":
			type asset struct {
				Name string `json:"name"`
				URL  string `json:"browser_download_url"`
			}
			rel := struct {
				TagName string  `json:"tag_name"`
				Body    string  `json:"body"`
				Assets  []asset `json:"assets"`
			}{TagName: f.tag, Body: "What's changed: things"}
			rel.Assets = append(rel.Assets, asset{assetName(runtime.GOOS, runtime.GOARCH), f.server.URL + "/bin"})
			if !f.noSums {
				rel.Assets = append(rel.Assets, asset{"SHA256SUMS", f.server.URL + "/sums"})
			}
			json.NewEncoder(w).Encode(rel)
		case "/bin":
			w.Write(f.binary)
		case "/sums":
			sums := f.sums
			if sums == "" {
				sum := sha256.Sum256(f.binary)
				sums = hex.EncodeToString(sum[:]) + "  " + asset + "\n"
			}
			w.Write([]byte(sums))
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(f.server.Close)
	return f
}

// updateApp is a fakeApp with a pretend installed binary at Exe, pointed at
// the fake release server. tried records the paths TryBinary was given.
func updateApp(t *testing.T, f *fakeRelease, stdin string) (a *App, tried *[]string) {
	t.Helper()
	a, _, _, _ = fakeApp(t, false)
	a.Stdin = strings.NewReader(stdin)
	a.ReleaseURL = f.server.URL + "/latest"
	binDir := t.TempDir()
	a.Exe = filepath.Join(binDir, "bothy")
	if err := os.WriteFile(a.Exe, []byte("old bothy"), 0o755); err != nil {
		t.Fatal(err)
	}
	var paths []string
	a.TryBinary = func(path string) error {
		paths = append(paths, path)
		return nil
	}
	return a, &paths
}

func out(a *App) string    { return a.Stdout.(interface{ String() string }).String() }
func errOut(a *App) string { return a.Stderr.(interface{ String() string }).String() }

func exeContent(t *testing.T, a *App) string {
	t.Helper()
	b, err := os.ReadFile(a.Exe)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// assertOnlyBinary checks the install folder holds just the binary: no temp
// file or backup left behind.
func assertOnlyBinary(t *testing.T, a *App) {
	t.Helper()
	entries, err := os.ReadDir(filepath.Dir(a.Exe))
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].Name() != "bothy" {
		var names []string
		for _, e := range entries {
			names = append(names, e.Name())
		}
		t.Errorf("install folder holds %v, want just bothy", names)
	}
}

func TestVersion(t *testing.T) {
	t.Run("a release build says when a newer release is out", func(t *testing.T) {
		setBuild(t, "v0.1.0", "", "")
		f := newFakeRelease(t, "v0.2.0")
		a, _ := updateApp(t, f, "")
		if code := a.Main([]string{"version"}); code != 0 {
			t.Fatalf("exit %d", code)
		}
		want := "bothy v0.1.0\nv0.2.0 is available. Run 'bothy self-update' to install it.\n"
		if out(a) != want {
			t.Errorf("got %q, want %q", out(a), want)
		}
	})

	t.Run("up to date", func(t *testing.T) {
		setBuild(t, "v0.2.0", "", "")
		a, _ := updateApp(t, newFakeRelease(t, "v0.2.0"), "")
		a.Main([]string{"version"})
		if want := "bothy v0.2.0\nNo newer release is out.\n"; out(a) != want {
			t.Errorf("got %q, want %q", out(a), want)
		}
	})

	t.Run("a source build shows its build date and points at git", func(t *testing.T) {
		setBuild(t, "v0.1.0-5-gabc1234", "yes", "2026-11-18")
		a, _ := updateApp(t, newFakeRelease(t, "v0.2.0"), "")
		a.Main([]string{"version"})
		want := "bothy v0.1.0-5-gabc1234 (built from source on 2026-11-18)\n" +
			"v0.2.0 is available. Update your clone with 'git pull', then run 'make install'.\n"
		if out(a) != want {
			t.Errorf("got %q, want %q", out(a), want)
		}
	})

	t.Run("a dev build doesn't look anything up", func(t *testing.T) {
		setBuild(t, "dev", "", "")
		f := newFakeRelease(t, "v0.2.0")
		a, _ := updateApp(t, f, "")
		a.Main([]string{"version"})
		if out(a) != "bothy dev\n" {
			t.Errorf("got %q", out(a))
		}
		if n := f.requests.Load(); n != 0 {
			t.Errorf("made %d requests, want none", n)
		}
	})

	t.Run("a failed lookup still prints the version and exits 0", func(t *testing.T) {
		setBuild(t, "v0.1.0", "", "")
		f := newFakeRelease(t, "v0.2.0")
		f.down = true
		a, _ := updateApp(t, f, "")
		if code := a.Main([]string{"version"}); code != 0 {
			t.Errorf("exit %d, want 0", code)
		}
		if out(a) != "bothy v0.1.0\n" {
			t.Errorf("got %q", out(a))
		}
	})
}

func TestSelfUpdate(t *testing.T) {
	t.Run("--yes replaces the binary with the checked download and tries it", func(t *testing.T) {
		setBuild(t, "v0.1.0", "", "")
		a, tried := updateApp(t, newFakeRelease(t, "v0.2.0"), "")
		if code := a.Main([]string{"self-update", "--yes"}); code != 0 {
			t.Fatalf("exit %d, stderr %q", code, errOut(a))
		}
		if got := exeContent(t, a); got != "new bothy v0.2.0" {
			t.Errorf("binary holds %q", got)
		}
		info, err := os.Stat(a.Exe)
		if err != nil {
			t.Fatal(err)
		}
		if info.Mode().Perm() != 0o755 {
			t.Errorf("mode %v, want 0755", info.Mode().Perm())
		}
		if len(*tried) != 1 || (*tried)[0] != a.Exe {
			t.Errorf("tried %v, want [%s]", *tried, a.Exe)
		}
		if !strings.Contains(out(a), "Updated to v0.2.0.") {
			t.Errorf("stdout %q", out(a))
		}
		assertOnlyBinary(t, a)
	})

	t.Run("asks first, showing the versions and release notes", func(t *testing.T) {
		setBuild(t, "v0.1.0", "", "")
		a, _ := updateApp(t, newFakeRelease(t, "v0.2.0"), "y\n")
		if code := a.Main([]string{"self-update"}); code != 0 {
			t.Fatalf("exit %d, stderr %q", code, errOut(a))
		}
		for _, want := range []string{"bothy v0.1.0 -> v0.2.0", "What's changed: things", "Update? [y/N]"} {
			if !strings.Contains(out(a), want) {
				t.Errorf("stdout lacks %q: %q", want, out(a))
			}
		}
		if got := exeContent(t, a); got != "new bothy v0.2.0" {
			t.Errorf("binary holds %q", got)
		}
	})

	t.Run("anything but yes leaves the binary alone and downloads nothing", func(t *testing.T) {
		setBuild(t, "v0.1.0", "", "")
		f := newFakeRelease(t, "v0.2.0")
		a, _ := updateApp(t, f, "\n")
		if code := a.Main([]string{"self-update"}); code != 0 {
			t.Errorf("exit %d", code)
		}
		if got := exeContent(t, a); got != "old bothy" {
			t.Errorf("binary holds %q", got)
		}
		if n := f.requests.Load(); n != 1 {
			t.Errorf("made %d requests, want 1 (the release lookup)", n)
		}
		assertOnlyBinary(t, a)
	})

	t.Run("without a terminal it needs --yes", func(t *testing.T) {
		setBuild(t, "v0.1.0", "", "")
		a, _ := updateApp(t, newFakeRelease(t, "v0.2.0"), "y\n")
		a.Interactive = false
		if code := a.Main([]string{"self-update"}); code != 1 {
			t.Errorf("exit %d, want 1", code)
		}
		if !strings.Contains(errOut(a), "--yes") {
			t.Errorf("stderr %q", errOut(a))
		}
		if got := exeContent(t, a); got != "old bothy" {
			t.Errorf("binary holds %q", got)
		}
	})

	t.Run("a binary that won't run is rolled back", func(t *testing.T) {
		setBuild(t, "v0.1.0", "", "")
		a, _ := updateApp(t, newFakeRelease(t, "v0.2.0"), "")
		a.TryBinary = func(path string) error {
			if got := exeContent(t, a); got != "new bothy v0.2.0" {
				t.Errorf("tried the binary while it held %q", got)
			}
			return errors.New("signal: killed")
		}
		if code := a.Main([]string{"self-update", "--yes"}); code != 1 {
			t.Errorf("exit %d, want 1", code)
		}
		if got := exeContent(t, a); got != "old bothy" {
			t.Errorf("binary holds %q, want the old one back", got)
		}
		for _, want := range []string{"old one is back", "signal: killed", "#build-from-source"} {
			if !strings.Contains(errOut(a), want) {
				t.Errorf("stderr lacks %q: %q", want, errOut(a))
			}
		}
		assertOnlyBinary(t, a)
	})

	t.Run("a download that doesn't match its checksum changes nothing", func(t *testing.T) {
		setBuild(t, "v0.1.0", "", "")
		f := newFakeRelease(t, "v0.2.0")
		f.sums = strings.Repeat("0", 64) + "  " + assetName(runtime.GOOS, runtime.GOARCH) + "\n"
		a, tried := updateApp(t, f, "")
		if code := a.Main([]string{"self-update", "--yes"}); code != 1 {
			t.Errorf("exit %d, want 1", code)
		}
		if got := exeContent(t, a); got != "old bothy" {
			t.Errorf("binary holds %q", got)
		}
		if len(*tried) != 0 {
			t.Error("tried a binary that failed its checksum")
		}
		if !strings.Contains(errOut(a), "doesn't match its checksum") {
			t.Errorf("stderr %q", errOut(a))
		}
		assertOnlyBinary(t, a)
	})

	t.Run("a release without SHA256SUMS changes nothing", func(t *testing.T) {
		setBuild(t, "v0.1.0", "", "")
		f := newFakeRelease(t, "v0.2.0")
		f.noSums = true
		a, _ := updateApp(t, f, "")
		if code := a.Main([]string{"self-update", "--yes"}); code != 1 {
			t.Errorf("exit %d, want 1", code)
		}
		if got := exeContent(t, a); got != "old bothy" {
			t.Errorf("binary holds %q", got)
		}
		if n := f.requests.Load(); n != 1 {
			t.Errorf("made %d requests, want 1 (the release lookup)", n)
		}
	})

	t.Run("already up to date", func(t *testing.T) {
		setBuild(t, "v0.2.0", "", "")
		a, _ := updateApp(t, newFakeRelease(t, "v0.2.0"), "")
		if code := a.Main([]string{"self-update", "--yes"}); code != 0 {
			t.Errorf("exit %d", code)
		}
		if !strings.Contains(out(a), "Already up to date (v0.2.0).") {
			t.Errorf("stdout %q", out(a))
		}
		if got := exeContent(t, a); got != "old bothy" {
			t.Errorf("binary holds %q", got)
		}
	})

	t.Run("a source build is left alone and pointed at git", func(t *testing.T) {
		setBuild(t, "v0.1.0", "yes", "2026-11-18")
		f := newFakeRelease(t, "v0.2.0")
		a, _ := updateApp(t, f, "")
		if code := a.Main([]string{"self-update", "--yes"}); code != 1 {
			t.Errorf("exit %d, want 1", code)
		}
		if !strings.Contains(errOut(a), "make install") {
			t.Errorf("stderr %q", errOut(a))
		}
		if got := exeContent(t, a); got != "old bothy" {
			t.Errorf("binary holds %q", got)
		}
		if n := f.requests.Load(); n != 1 {
			t.Errorf("made %d requests, want 1 (the release lookup)", n)
		}
	})

	t.Run("a dev build refuses without looking anything up", func(t *testing.T) {
		setBuild(t, "dev", "", "")
		f := newFakeRelease(t, "v0.2.0")
		a, _ := updateApp(t, f, "")
		if code := a.Main([]string{"self-update", "--yes"}); code != 1 {
			t.Errorf("exit %d, want 1", code)
		}
		if !strings.Contains(errOut(a), "go install github.com/ohnotnow/bothy@latest") {
			t.Errorf("stderr %q", errOut(a))
		}
		if n := f.requests.Load(); n != 0 {
			t.Errorf("made %d requests, want none", n)
		}
		if got := exeContent(t, a); got != "old bothy" {
			t.Errorf("binary holds %q", got)
		}
	})
}

func TestSelfUpdateCheck(t *testing.T) {
	cases := []struct {
		name    string
		version string
		args    []string
		down    bool
		want    int
	}{
		{"up to date", "v0.2.0", []string{"self-update", "--check"}, false, 0},
		{"newer available", "v0.1.0", []string{"self-update", "--check"}, false, 1},
		{"lookup failed", "v0.1.0", []string{"self-update", "--check"}, true, 2},
		{"dev build can't tell", "dev", []string{"self-update", "--check"}, false, 2},
		{"--dry-run only checks", "v0.1.0", []string{"--dry-run", "self-update", "--yes"}, false, 1},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			setBuild(t, c.version, "", "")
			f := newFakeRelease(t, "v0.2.0")
			f.down = c.down
			a, _ := updateApp(t, f, "y\n")
			if code := a.Main(c.args); code != c.want {
				t.Errorf("exit %d, want %d (stdout %q, stderr %q)", code, c.want, out(a), errOut(a))
			}
			if got := exeContent(t, a); got != "old bothy" {
				t.Errorf("binary holds %q", got)
			}
		})
	}
}

func TestTryBinary(t *testing.T) {
	dir := t.TempDir()
	write := func(name, script string) string {
		path := filepath.Join(dir, name)
		if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
			t.Fatal(err)
		}
		return path
	}
	if err := tryBinary(write("works", "#!/bin/sh\n[ \"$1\" = help ] || exit 9\necho usage\n")); err != nil {
		t.Errorf("a binary that runs: %v", err)
	}
	err := tryBinary(write("fails", "#!/bin/sh\necho blocked by policy >&2\nexit 1\n"))
	if err == nil || !strings.Contains(err.Error(), "blocked by policy") {
		t.Errorf("a binary that fails: got %v, want an error carrying its output", err)
	}
	if err := tryBinary(write("garbage", "not a program")); err == nil {
		t.Error("a file that isn't a program: got no error")
	}
}
