package bothy

import (
	"bytes"
	"reflect"
	"strings"
	"testing"
)

// Shaped like real `docker compose ps --format json` output (one object per
// line), trimmed to the fields bothy reads.
const psOutput = `{"Service":"appserver","State":"running","Labels":"com.docker.compose.project=alpha-dev,traefik.enable=true,traefik.http.routers.alpha.rule=Host(` + "`alpha.bothy.localhost`" + `),traefik.http.services.alpha.loadbalancer.server.port=6060","Publishers":[{"URL":"127.0.0.1","TargetPort":6060,"PublishedPort":54123,"Protocol":"tcp"}]}
{"Service":"cache","State":"running","Publishers":[{"URL":"","TargetPort":6379,"PublishedPort":0,"Protocol":"tcp"}]}
{"Service":"database","State":"running","Publishers":[{"URL":"127.0.0.1","TargetPort":3306,"PublishedPort":4406,"Protocol":"tcp"}]}
{"Service":"sendmailhog","State":"running","Publishers":[{"URL":"0.0.0.0","TargetPort":8025,"PublishedPort":54124,"Protocol":"tcp"},{"URL":"::","TargetPort":8025,"PublishedPort":54124,"Protocol":"tcp"},{"URL":"","TargetPort":1025,"PublishedPort":0,"Protocol":"tcp"}]}
`

func TestParsePublished(t *testing.T) {
	got, hosts, err := parsePublished([]byte(psOutput))
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(hosts, []string{"alpha.bothy.localhost"}) {
		t.Fatalf("hosts %q", hosts)
	}
	want := []published{
		{"appserver", "127.0.0.1", 54123, 6060},
		{"database", "127.0.0.1", 4406, 3306},
		{"sendmailhog", "0.0.0.0", 54124, 8025},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %+v, want %+v", got, want)
	}
}

// infoApp answers compose ps with psOutput and the database question with db.
func infoApp(t *testing.T, db string) (*App, *[][]string, *bytes.Buffer) {
	t.Helper()
	a, calls, stdout, _ := fakeApp(t, true)
	a.Output = func(args []string) ([]byte, error) {
		joined := strings.Join(args, " ")
		switch {
		case strings.HasPrefix(joined, "compose ls"):
			return []byte(proxyUp), nil
		case strings.Contains(joined, " ps "):
			return []byte(psOutput), nil
		}
		return []byte(db), nil
	}
	return a, calls, stdout
}

func TestInfo(t *testing.T) {
	a, _, out := infoApp(t, "laravel\nlaravel\nsecret with spaces\n")
	if code := a.Main([]string{"info"}); code != 0 {
		t.Fatalf("exit %d", code)
	}
	got := out.String()
	for _, s := range []string{
		"http://alpha.bothy.localhost",
		"http://127.0.0.1:54123",
		"127.0.0.1:4406  (database laravel, user laravel, password secret with spaces)",
		"http://127.0.0.1:54124",
	} {
		if !strings.Contains(got, s) {
			t.Errorf("missing %q in:\n%s", s, got)
		}
	}
	if strings.Contains(got, "http://127.0.0.1:4406") || strings.Contains(got, "cache") {
		t.Errorf("unexpected line in:\n%s", got)
	}
}

func TestInfoWithoutDbDetails(t *testing.T) {
	a, _, out := infoApp(t, "")
	a.Main([]string{"info"})
	if !strings.Contains(out.String(), "127.0.0.1:4406\n") {
		t.Fatalf("got:\n%s", out)
	}
}

func TestStartPrintsInfo(t *testing.T) {
	a, calls, out := infoApp(t, "laravel\nlaravel\nsecret\n")
	if code := a.Main([]string{"start"}); code != 0 {
		t.Fatalf("exit %d", code)
	}
	if len(*calls) != 1 {
		t.Fatalf("calls %q", *calls)
	}
	if !strings.Contains(out.String(), "http://127.0.0.1:54123") {
		t.Fatalf("start did not print the addresses:\n%s", out)
	}
}

func TestStartDoesNotPrintInfoWhenItFails(t *testing.T) {
	a, _, out := infoApp(t, "")
	a.Run = func([]string) int { return 1 }
	if code := a.Main([]string{"start"}); code != 1 {
		t.Fatalf("exit %d, want 1", code)
	}
	if out.Len() != 0 {
		t.Fatalf("printed %q", out)
	}
}
