package bothy

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

// watchApp is fakeApp with a resources/ folder and an Npm that records its
// calls and returns the next code from codes (0 when exhausted).
func watchApp(t *testing.T, codes ...int) (*App, *[][]string) {
	t.Helper()
	a, _, _, _ := fakeApp(t, true)
	writeFile(t, filepath.Join(a.Dir, "resources", "css", "app.css"), "body {}")
	var calls [][]string
	a.Npm = func(args ...string) int {
		calls = append(calls, args)
		if len(codes) == 0 {
			return 0
		}
		c := codes[0]
		codes = codes[1:]
		return c
	}
	a.Now = func() time.Time { return time.Date(2026, 11, 21, 14, 3, 22, 0, time.UTC) }
	return a, &calls
}

func writeFile(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestWatchRejectsBadArguments(t *testing.T) {
	for _, args := range []string{"watch --every 0", "watch --every -1", "watch --every soon", "watch --every NaN", "watch --every Inf", "watch now", "watch --often"} {
		t.Run(args, func(t *testing.T) {
			a, calls := watchApp(t)
			if code := a.Main(words(args)); code != exitUsage {
				t.Errorf("exit %d, want %d", code, exitUsage)
			}
			if len(*calls) != 0 {
				t.Errorf("npm ran: %v", *calls)
			}
		})
	}
}

func TestWatchNeedsResourcesAndNpm(t *testing.T) {
	t.Run("no resources folder", func(t *testing.T) {
		a, calls := watchApp(t)
		os.RemoveAll(filepath.Join(a.Dir, "resources"))
		if code := a.Main(words("watch")); code != exitNotFound {
			t.Errorf("exit %d, want %d", code, exitNotFound)
		}
		if len(*calls) != 0 {
			t.Errorf("npm ran: %v", *calls)
		}
	})
	t.Run("no npm", func(t *testing.T) {
		a, _ := watchApp(t)
		a.Npm = nil
		var stderr strings.Builder
		a.Stderr = &stderr
		if code := a.Main(words("watch")); code != 1 {
			t.Errorf("exit %d, want 1", code)
		}
		if !strings.Contains(stderr.String(), "npm isn't on your PATH") {
			t.Errorf("stderr = %q", stderr.String())
		}
	})
}

func TestWatchDryRun(t *testing.T) {
	a, calls := watchApp(t)
	var stdout strings.Builder
	a.Stdout = &stdout
	if code := a.Main(words("--dry-run watch --every 0.5")); code != 0 {
		t.Fatalf("exit %d", code)
	}
	if len(*calls) != 0 {
		t.Errorf("npm ran: %v", *calls)
	}
	if want := "npm run build  # then again whenever resources/ changes, checked every 0.5s\n"; stdout.String() != want {
		t.Errorf("stdout = %q, want %q", stdout.String(), want)
	}
}

func TestWatchLoop(t *testing.T) {
	a, calls := watchApp(t, 1) // the first build fails; watching carries on
	var stdout strings.Builder
	a.Stdout = &stdout
	res := filepath.Join(a.Dir, "resources")
	css := filepath.Join(res, "css", "app.css")
	view := filepath.Join(res, "views", "home.blade.php")
	later := time.Now().Add(time.Hour)

	// each step changes the files (or not), then the loop checks them
	steps := []struct {
		name   string
		change func()
		builds int // total builds expected after this step's check
	}{
		{"nothing changed", func() {}, 1},
		{"a new view", func() { writeFile(t, view, "<p>hi</p>") }, 2},
		{"an editor swap file", func() { writeFile(t, filepath.Join(res, "views", ".home.blade.php.swp"), "x") }, 2},
		{"a file in a dot folder", func() { writeFile(t, filepath.Join(res, ".cache", "x"), "x") }, 2},
		{"a touched file, same size", func() {
			if err := os.Chtimes(css, later, later); err != nil {
				t.Fatal(err)
			}
		}, 3},
		{"two files at once", func() {
			writeFile(t, css, "body { color: red }")
			writeFile(t, view, "<p>hello</p>")
		}, 4},
		{"a deleted file", func() { os.Remove(view) }, 5},
	}
	i := 0
	wait := func() bool {
		if i > 0 && len(*calls) != steps[i-1].builds {
			t.Errorf("after %q: %d builds, want %d", steps[i-1].name, len(*calls), steps[i-1].builds)
		}
		if i == len(steps) {
			return false
		}
		steps[i].change()
		i++
		return true
	}

	if code := a.watchLoop(res, wait); code != 0 {
		t.Errorf("exit %d", code)
	}
	for _, c := range *calls {
		if !reflect.DeepEqual(c, []string{"run", "build"}) {
			t.Errorf("npm called with %v", c)
		}
	}
	for _, want := range []string{
		"14:03:22  resources/views/home.blade.php changed, rebuilding",
		"14:03:22  resources/css/app.css changed, rebuilding",
		"14:03:22  resources/css/app.css and 1 more changed, rebuilding",
	} {
		if !strings.Contains(stdout.String(), want) {
			t.Errorf("stdout lacks %q:\n%s", want, stdout.String())
		}
	}
}
