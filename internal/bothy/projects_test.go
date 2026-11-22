package bothy

import (
	"errors"
	"reflect"
	"strings"
	"testing"
)

// Shaped like real `docker compose ls --format json` output: a lando project
// (many generated files, none named compose.dev.yml) and two bothy ones.
const lsOutput = `[
 {"Name":"landoapp","Status":"running(5)","ConfigFiles":"/home/dev/.lando/compose/landoapp/globals-0.yml,/home/dev/.lando/compose/landoapp/appserver-0.yml"},
 {"Name":"alpha-dev","Status":"running(4)","ConfigFiles":"/home/dev/code/alpha/compose.dev.yml"},
 {"Name":"beta-dev","Status":"running(3)","ConfigFiles":"/home/dev/code/beta/compose.dev.yml"}
]`

func TestParseProjectsKeepsOnlyBothyOnes(t *testing.T) {
	got, err := parseProjects([]byte(lsOutput))
	if err != nil {
		t.Fatal(err)
	}
	want := []project{
		{"alpha-dev", "running(4)", "/home/dev/code/alpha/compose.dev.yml"},
		{"beta-dev", "running(3)", "/home/dev/code/beta/compose.dev.yml"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %+v, want %+v", got, want)
	}
}

func TestLs(t *testing.T) {
	a, _, stdout, _ := fakeApp(t, false)
	a.Output = func([]string) ([]byte, error) { return []byte(lsOutput), nil }
	if code := a.Main([]string{"ls"}); code != 0 {
		t.Fatalf("exit %d", code)
	}
	out := stdout.String()
	for _, s := range []string{"alpha-dev", "/home/dev/code/beta"} {
		if !strings.Contains(out, s) {
			t.Errorf("output missing %q:\n%s", s, out)
		}
	}
	if strings.Contains(out, "landoapp") {
		t.Errorf("output lists a lando project:\n%s", out)
	}
}

func TestPoweroffStopsEachBothyProject(t *testing.T) {
	a, calls, _, _ := fakeApp(t, false, 1, 0)
	a.Output = func([]string) ([]byte, error) { return []byte(lsOutput), nil }
	if code := a.Main([]string{"poweroff"}); code != 1 {
		t.Fatalf("exit %d, want 1 because the first stop failed", code)
	}
	want := [][]string{
		{"compose", "-p", "alpha-dev", "-f", "/home/dev/code/alpha/compose.dev.yml", "down"},
		{"compose", "-p", "beta-dev", "-f", "/home/dev/code/beta/compose.dev.yml", "down"},
	}
	if !reflect.DeepEqual(*calls, want) {
		t.Fatalf("got %q, want %q", *calls, want)
	}
}

func TestLsWhenDockerFails(t *testing.T) {
	a, _, _, stderr := fakeApp(t, false)
	a.Output = func([]string) ([]byte, error) { return nil, errors.New("daemon not running") }
	if code := a.Main([]string{"ls"}); code != 1 {
		t.Fatalf("exit %d, want 1", code)
	}
	if !strings.Contains(stderr.String(), "daemon not running") {
		t.Fatalf("stderr %q", stderr)
	}
}
