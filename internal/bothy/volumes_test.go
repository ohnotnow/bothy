package bothy

import (
	"reflect"
	"strings"
	"testing"
)

// Shaped like real `docker system df -v --format json` output, trimmed to
// the fields bothy reads: a lando volume, a non-bothy compose volume and two
// bothy ones.
const dfOutput = `{"Images":[],"Containers":[],"BuildCache":[],"Volumes":[
 {"Name":"landoapp_data_database","Labels":"com.docker.compose.project=landoapp,com.docker.compose.volume=data_database","Size":"317.4MB","Links":"1"},
 {"Name":"alpha_mariadb","Labels":"com.docker.compose.config-hash=abc,com.docker.compose.project=alpha,com.docker.compose.volume=mariadb","Size":"157.5MB","Links":"0"},
 {"Name":"beta-dev_database","Labels":"com.docker.compose.project=beta-dev,com.docker.compose.volume=database","Size":"12MB","Links":"1"},
 {"Name":"alpha-dev_database","Labels":"com.docker.compose.config-hash=def,com.docker.compose.project=alpha-dev,com.docker.compose.volume=database","Size":"211.6MB","Links":"0"},
 {"Name":"anonymous123","Labels":"","Size":"0B","Links":"0"}
]}`

func TestParseVolumesKeepsOnlyBothyOnes(t *testing.T) {
	got, err := parseVolumes([]byte(dfOutput))
	if err != nil {
		t.Fatal(err)
	}
	want := []volume{
		{"alpha-dev_database", "alpha-dev", "211.6MB", false},
		{"beta-dev_database", "beta-dev", "12MB", true},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %+v, want %+v", got, want)
	}
}

func TestVolumes(t *testing.T) {
	a, calls, stdout, _ := fakeApp(t, false)
	a.Output = func([]string) ([]byte, error) { return []byte(dfOutput), nil }
	if code := a.Main([]string{"volumes"}); code != 0 {
		t.Fatalf("exit %d", code)
	}
	if len(*calls) != 0 {
		t.Fatalf("volumes ran a docker command that changes things: %q", *calls)
	}
	out := stdout.String()
	if !strings.Contains(out, "alpha-dev_database") || strings.Contains(out, "alpha_mariadb") {
		t.Fatalf("unexpected output:\n%s", out)
	}
}
