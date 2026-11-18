package bothy

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"time"
)

// RepoURL is where bothy's releases are published.
const RepoURL = "https://github.com/ohnotnow/bothy"

// LatestReleaseAPI is GitHub's API address for bothy's newest release.
const LatestReleaseAPI = "https://api.github.com/repos/ohnotnow/bothy/releases/latest"

// Built and FromSource are set by `make install`: the build date, and "yes"
// for a binary built from a clone rather than downloaded from a release.
// Self-update leaves source builds alone; their owners update with git.
var (
	Built      = ""
	FromSource = ""
)

const (
	checkTimeout    = 5 * time.Second
	downloadTimeout = 2 * time.Minute
	tryTimeout      = 30 * time.Second
)

// versionLine is the first line of `bothy version`.
func versionLine() string {
	line := "bothy " + Version
	if FromSource != "" {
		line += " (built from source"
		if Built != "" {
			line += " on " + Built
		}
		line += ")"
	}
	return line
}

// updateHint says how this binary gets updated.
func updateHint() string {
	if FromSource != "" {
		return "Update your clone with 'git pull', then run 'make install'."
	}
	return "Run 'bothy self-update' to install it."
}

func cmdVersion(a *App, _ []string) int {
	fmt.Fprintln(a.Stdout, versionLine())
	if a.Skills != nil {
		fmt.Fprintln(a.Stdout, skillsStatus(a.Skills, a.ClaudeDir))
	}
	// A dev build has no version to compare. A failed lookup stays quiet:
	// the version is what was asked for.
	if Version == "dev" || a.ReleaseURL == "" {
		return 0
	}
	rel, err := fetchRelease(a.ReleaseURL, checkTimeout)
	if err != nil {
		return 0
	}
	if isNewer(rel.TagName, Version) {
		fmt.Fprintf(a.Stdout, "%s is available. %s\n", rel.TagName, updateHint())
	} else {
		fmt.Fprintln(a.Stdout, "No newer release is out.")
	}
	return 0
}

func cmdSelfUpdate(a *App, args []string) int {
	fs := flag.NewFlagSet("self-update", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	check := fs.Bool("check", false, "")
	var yes bool
	fs.BoolVar(&yes, "yes", false, "")
	fs.BoolVar(&yes, "y", false, "")
	if err := fs.Parse(args); err != nil || fs.NArg() > 0 {
		return a.usageError("usage: bothy self-update [--check] [--yes]")
	}
	// --dry-run downloads nothing and changes nothing, which is --check.
	if a.dryRun {
		*check = true
	}

	// --check exits 0 when up to date, 1 when a newer release is out, and
	// 2 when it can't tell, so scripts can act on it.
	failed := 1
	if *check {
		failed = 2
	}

	if Version == "dev" {
		fmt.Fprintln(a.Stderr, "bothy: this is a dev build (made with 'go install' or a plain 'go build'), so self-update is off.")
		fmt.Fprintln(a.Stderr, "Update it the way you built it: 'go install github.com/ohnotnow/bothy@latest', or 'git pull' then 'make install' in your clone.")
		return failed
	}

	rel, err := fetchRelease(a.ReleaseURL, checkTimeout)
	if err != nil {
		fmt.Fprintf(a.Stderr, "bothy: checking for a new release: %v\n", err)
		return failed
	}
	if !isNewer(rel.TagName, Version) {
		fmt.Fprintf(a.Stdout, "Already up to date (%s).\n", Version)
		return 0
	}
	if *check {
		fmt.Fprintf(a.Stdout, "bothy %s is installed; %s is available.\n", Version, rel.TagName)
		return 1
	}
	if FromSource != "" {
		fmt.Fprintf(a.Stderr, "bothy: %s is available, but this bothy was built from source, so self-update leaves it alone.\n", rel.TagName)
		fmt.Fprintln(a.Stderr, updateHint())
		return 1
	}

	asset := assetName(runtime.GOOS, runtime.GOARCH)
	if asset == "" {
		fmt.Fprintf(a.Stderr, "bothy: there is no release build for %s/%s\n", runtime.GOOS, runtime.GOARCH)
		return 1
	}
	binURL, ok := rel.assetURL(asset)
	if !ok {
		fmt.Fprintf(a.Stderr, "bothy: release %s has no %s\n", rel.TagName, asset)
		return 1
	}
	sumsURL, ok := rel.assetURL("SHA256SUMS")
	if !ok {
		fmt.Fprintf(a.Stderr, "bothy: release %s has no SHA256SUMS to check the download against\n", rel.TagName)
		return 1
	}
	if a.Exe == "" {
		fmt.Fprintln(a.Stderr, "bothy: can't find where this bothy binary is, so self-update can't replace it")
		return 1
	}
	// Find out now, not after the download, if the binary can't be replaced.
	if dir := filepath.Dir(a.Exe); !canWrite(dir) {
		fmt.Fprintf(a.Stderr, "bothy: can't write to %s, so self-update can't replace %s. Download the new release from %s/releases/latest\n", dir, a.Exe, RepoURL)
		return 1
	}

	if !yes {
		if !a.Interactive {
			fmt.Fprintln(a.Stderr, "bothy: self-update asks before replacing bothy, and there is no terminal to ask on. Run 'bothy self-update --yes' to update without asking.")
			return 1
		}
		fmt.Fprintf(a.Stdout, "bothy %s -> %s\n", Version, rel.TagName)
		if notes := strings.TrimSpace(rel.Body); notes != "" {
			fmt.Fprintf(a.Stdout, "\nRelease notes:\n%s\n\n", notes)
		}
		if !a.askYes("Update?") {
			fmt.Fprintln(a.Stdout, "Not updated.")
			return 0
		}
	}

	bin, err := download(binURL, downloadTimeout)
	if err != nil {
		fmt.Fprintf(a.Stderr, "bothy: downloading %s: %v\n", asset, err)
		return 1
	}
	sums, err := download(sumsURL, downloadTimeout)
	if err != nil {
		fmt.Fprintf(a.Stderr, "bothy: downloading SHA256SUMS: %v\n", err)
		return 1
	}
	want, ok := lookupChecksum(string(sums), asset)
	if !ok {
		fmt.Fprintf(a.Stderr, "bothy: SHA256SUMS in release %s has no line for %s\n", rel.TagName, asset)
		return 1
	}
	sum := sha256.Sum256(bin)
	if got := hex.EncodeToString(sum[:]); !strings.EqualFold(got, want) {
		fmt.Fprintf(a.Stderr, "bothy: the download of %s doesn't match its checksum (expected %s, got %s); nothing was changed\n", asset, want, got)
		return 1
	}

	if err := a.replaceExe(bin); err != nil {
		var rb *rolledBackError
		if errors.As(err, &rb) {
			fmt.Fprintf(a.Stderr, "bothy: the new bothy (%s) wouldn't run on this machine, so the old one is back in place.\n", rel.TagName)
			fmt.Fprintf(a.Stderr, "  %v\n", rb.err)
			fmt.Fprintf(a.Stderr, "This machine may block binaries downloaded from the internet. Build bothy from source instead: %s#build-from-source\n", RepoURL)
			return 1
		}
		fmt.Fprintf(a.Stderr, "bothy: replacing %s: %v\n", a.Exe, err)
		return 1
	}
	fmt.Fprintf(a.Stdout, "Updated to %s.\n", rel.TagName)
	fmt.Fprintln(a.Stdout, "The skill may have changed too. Run 'bothy install-skills --dry-run' to see.")
	return 0
}

// rolledBackError means the new binary was put in place, failed to run, and
// the old one was put back.
type rolledBackError struct{ err error }

func (e *rolledBackError) Error() string { return "new binary failed to run: " + e.err.Error() }

// replaceExe swaps the running binary for content, then runs the new one
// once and puts the old one back if it fails. Managed Macs may refuse to run
// a downloaded binary, and finding that out now beats finding it out at the
// next `bothy start`.
//
// The new binary goes in under a fresh name and is renamed over the old one:
// macOS kills a binary whose file was overwritten in place, and the rename
// is atomic. The old binary is kept as a hard link until the new one has run.
func (a *App) replaceExe(content []byte) error {
	dir, base := filepath.Dir(a.Exe), filepath.Base(a.Exe)

	tmp, err := os.CreateTemp(dir, "."+base+".new-*")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	defer os.Remove(tmpPath) // a no-op once it has been renamed
	if _, err := tmp.Write(content); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Chmod(0o755); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}

	backup := filepath.Join(dir, "."+base+".old")
	_ = os.Remove(backup) // left over from an interrupted update
	if err := os.Link(a.Exe, backup); err != nil {
		return fmt.Errorf("keeping a copy of the old binary: %w", err)
	}
	if err := os.Rename(tmpPath, a.Exe); err != nil {
		_ = os.Remove(backup)
		return err
	}

	if tryErr := a.TryBinary(a.Exe); tryErr != nil {
		if err := os.Rename(backup, a.Exe); err != nil {
			return fmt.Errorf("the new binary failed to run (%v), and putting the old one back failed too (%v); the old one is at %s", tryErr, err, backup)
		}
		return &rolledBackError{tryErr}
	}
	return os.Remove(backup)
}

// tryBinary runs `<path> help` and reports whether it worked.
func tryBinary(path string) error {
	ctx, cancel := context.WithTimeout(context.Background(), tryTimeout)
	defer cancel()
	out, err := exec.CommandContext(ctx, path, "help").CombinedOutput()
	if err != nil {
		if msg := strings.TrimSpace(string(out)); msg != "" {
			return fmt.Errorf("%w: %s", err, msg)
		}
		return err
	}
	return nil
}

// release is the part of GitHub's release JSON that bothy reads.
type release struct {
	TagName string `json:"tag_name"`
	Body    string `json:"body"`
	Assets  []struct {
		Name string `json:"name"`
		URL  string `json:"browser_download_url"`
	} `json:"assets"`
}

func (r *release) assetURL(name string) (string, bool) {
	for _, a := range r.Assets {
		if a.Name == name {
			return a.URL, true
		}
	}
	return "", false
}

func fetchRelease(url string, timeout time.Duration) (*release, error) {
	body, err := download(url, timeout)
	if err != nil {
		return nil, err
	}
	var rel release
	if err := json.Unmarshal(body, &rel); err != nil {
		return nil, err
	}
	return &rel, nil
}

func download(url string, timeout time.Duration) ([]byte, error) {
	client := &http.Client{Timeout: timeout}
	resp, err := client.Get(url)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("%s answered %s", url, resp.Status)
	}
	return io.ReadAll(resp.Body)
}

// assetName is the release file for a platform, or "" if there isn't one.
// Keep in step with the builds in .github/workflows/release.yml.
func assetName(goos, goarch string) string {
	if (goos == "darwin" || goos == "linux") && (goarch == "amd64" || goarch == "arm64") {
		return "bothy-" + goos + "-" + goarch
	}
	return ""
}

// lookupChecksum finds name's digest in a sha256sum listing, whose lines are
// "<hex>  <name>" (or "<hex> *<name>" in binary mode).
func lookupChecksum(sums, name string) (string, bool) {
	for _, line := range strings.Split(sums, "\n") {
		fields := strings.Fields(line)
		if len(fields) == 2 && strings.TrimPrefix(fields[1], "*") == name {
			return fields[0], true
		}
	}
	return "", false
}

// canWrite reports whether a file can be created in dir.
func canWrite(dir string) bool {
	f, err := os.CreateTemp(dir, ".bothy-write-test-*")
	if err != nil {
		return false
	}
	f.Close()
	os.Remove(f.Name())
	return true
}

// versionPrefix matches the release a version is based on: v0.2.0 itself,
// or the start of a source build's v0.2.0-5-gabc1234 or v0.2.0-dirty.
var versionPrefix = regexp.MustCompile(`^v?(\d+)\.(\d+)\.(\d+)`)

// isNewer reports whether latest is a later release than the one current is
// based on. Versions it can't read are never newer.
func isNewer(latest, current string) bool {
	l, okL := parseVersion(latest)
	c, okC := parseVersion(current)
	if !okL || !okC {
		return false
	}
	for i := range l {
		if l[i] != c[i] {
			return l[i] > c[i]
		}
	}
	return false
}

func parseVersion(v string) ([3]int, bool) {
	m := versionPrefix.FindStringSubmatch(v)
	if m == nil {
		return [3]int{}, false
	}
	var out [3]int
	for i := range out {
		n, err := strconv.Atoi(m[i+1])
		if err != nil {
			return [3]int{}, false
		}
		out[i] = n
	}
	return out, true
}
