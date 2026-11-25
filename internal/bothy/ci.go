package bothy

import (
	"bufio"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

// bothy ci runs, locally, the checks the team's GitLab pipelines run in
// their test stage: env keys, stray dd/dump calls, composer audit, pint and
// the tests. Every check runs, so one round of fixes covers them all; the
// output streams, so an early failure shows at once. It is nearly as good as
// CI, not CI: the GitLab-only checks (env keys against the QA and production
// secrets) and the image build aren't here.

// The tests run against a database of bothy's own in the app's MariaDB,
// through a user whose rights stop at bothy_ci and the bothy_ci_test_N
// copies parallel testing makes. A test pointed at the developer's database
// by mistake gets "access denied", not a wipe.
const (
	ciDatabase = "bothy_ci"
	// ciCreating is printed by the setup SQL itself, only on the run that
	// makes the database, so it has to come before the CREATE.
	ciCreating = "Creating the bothy_ci database and user for the tests (first run only; bothy destroy removes them)."
	ciSetupSQL = "SELECT '" + ciCreating + "' FROM DUAL WHERE NOT EXISTS (SELECT 1 FROM information_schema.schemata WHERE schema_name = 'bothy_ci');\n" +
		"CREATE DATABASE IF NOT EXISTS `bothy_ci`;\n" +
		"CREATE USER IF NOT EXISTS 'bothy_ci'@'%' IDENTIFIED BY 'bothy_ci';\n" +
		"GRANT ALL PRIVILEGES ON `bothy\\_ci%`.* TO 'bothy_ci'@'%';\n"
	// -N: no column heading above the ciCreating line
	ciSetup = asRoot + "mariadb -uroot -N"
)

// ciTestEnv points the tests at the bothy_ci database. These win over plain
// <env> lines in phpunit.xml and over the app's .env. DB_CONNECTION is mysql
// because older Laravel has no mariadb connection, and mysql talks to MariaDB
// fine. The url variables are emptied because Laravel builds the connection
// from a url when one is set, and ignores an empty one.
var ciTestEnv = []string{
	"DB_CONNECTION=mysql",
	"DB_HOST=" + dbService,
	"DB_PORT=3306",
	"DB_DATABASE=" + ciDatabase,
	"DB_USERNAME=" + ciDatabase,
	"DB_PASSWORD=" + ciDatabase,
	"DB_URL=",
	"DATABASE_URL=",
}

// strayDebug is the team's CI check for a forgotten dd() or dump().
var strayDebug = regexp.MustCompile(`[^a-zA-Z](dd|dump)\(`)

type ciStatus int

const (
	ciPassed ciStatus = iota
	ciFailed
	ciSkipped
)

func (s ciStatus) String() string {
	return [...]string{"passed", "FAILED", "skipped"}[s]
}

type ciCheck struct {
	name string
	run  func(a *App) (ciStatus, int)
}

// exitInterrupted is what docker returns when ctrl-c stopped the command.
const exitInterrupted = 130

func cmdCi(a *App, args []string) int {
	if len(args) > 0 {
		return a.usageError("ci takes no arguments")
	}
	if code := a.refuseCachedConfig(); code != 0 {
		return code
	}
	if !a.dryRun {
		if code := a.needRunning(appService, dbService); code != 0 {
			return code
		}
	}
	checks := []ciCheck{
		{"env keys", (*App).ciEnvKeys},
		{"stray dd/dump", (*App).ciStrayDebug},
		{"composer audit", (*App).ciAudit},
		{"pint", (*App).ciPint},
		{"tests", (*App).ciTests},
	}
	results := make([]ciStatus, len(checks))
	for i, c := range checks {
		fmt.Fprintf(a.Stdout, "==> %s\n", c.name)
		status, code := c.run(a)
		if code == exitInterrupted {
			fmt.Fprintln(a.Stderr, "bothy: ci stopped")
			return exitInterrupted
		}
		results[i] = status
		fmt.Fprintln(a.Stdout)
	}
	if a.dryRun {
		return 0
	}

	failed := 0
	for _, r := range results {
		if r == ciFailed {
			failed++
		}
	}
	if failed == 0 {
		fmt.Fprintln(a.Stdout, "bothy ci: all checks passed")
	} else {
		fmt.Fprintf(a.Stdout, "bothy ci: %d of %d checks failed\n", failed, len(checks))
	}
	for i, c := range checks {
		fmt.Fprintf(a.Stdout, "  %-8s %s\n", results[i], c.name)
	}
	fmt.Fprintln(a.Stdout, "These are the checks the team's CI runs most; CI may still catch something they don't.")
	if failed > 0 {
		return 1
	}
	return 0
}

// needRunning refuses unless each service is running in the app's stack.
func (a *App) needRunning(services ...string) int {
	out, err := a.Output(compose("ps", "--status", "running", "--services"))
	if err != nil {
		fmt.Fprintf(a.Stderr, "bothy: asking docker what is running: %v\n", err)
		return 1
	}
	running := strings.Fields(string(out))
	for _, s := range services {
		found := false
		for _, r := range running {
			found = found || r == s
		}
		if !found {
			fmt.Fprintf(a.Stderr, "bothy: %s isn't running (bothy start starts it)\n", s)
			return 1
		}
	}
	return 0
}

// ciEnvKeys compares the key names in .env and .env.example, both ways.
// bothy's own BOTHY_* keys are left out: they belong in .env only. It prints
// key names and never a value, since the output may go to an agent.
func (a *App) ciEnvKeys() (ciStatus, int) {
	example, err := os.ReadFile(filepath.Join(a.Dir, ".env.example"))
	if err != nil {
		fmt.Fprintln(a.Stdout, "No .env.example to compare with.")
		return ciSkipped, 0
	}
	env, err := os.ReadFile(filepath.Join(a.Dir, ".env"))
	if err != nil {
		fmt.Fprintln(a.Stdout, "No .env: copy .env.example to .env and fill it in.")
		return ciFailed, 0
	}
	missing, extra := keyDiff(envKeys(example), envKeys(env))
	if len(missing)+len(extra) == 0 {
		fmt.Fprintln(a.Stdout, ".env and .env.example have the same keys.")
		return ciPassed, 0
	}
	if len(missing) > 0 {
		fmt.Fprintln(a.Stdout, "In .env.example but not in your .env:")
		for _, k := range missing {
			fmt.Fprintln(a.Stdout, "  "+k)
		}
	}
	if len(extra) > 0 {
		fmt.Fprintln(a.Stdout, "In your .env but not in .env.example (add them there, even with an empty value):")
		for _, k := range extra {
			fmt.Fprintln(a.Stdout, "  "+k)
		}
	}
	return ciFailed, 0
}

var envKeyName = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

// envKeys returns the key names a dotenv file sets, leaving out BOTHY_*.
// Lines that don't start with a plain KEY= are passed over.
func envKeys(data []byte) map[string]bool {
	keys := map[string]bool{}
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		line = strings.TrimPrefix(line, "export ")
		k, _, ok := strings.Cut(line, "=")
		k = strings.TrimSpace(k)
		if !ok || !envKeyName.MatchString(k) || strings.HasPrefix(k, "BOTHY_") {
			continue
		}
		keys[k] = true
	}
	return keys
}

// keyDiff returns, sorted, the keys in want that have is missing and the
// keys in have that want lacks.
func keyDiff(want, have map[string]bool) (missing, extra []string) {
	for k := range want {
		if !have[k] {
			missing = append(missing, k)
		}
	}
	for k := range have {
		if !want[k] {
			extra = append(extra, k)
		}
	}
	sort.Strings(missing)
	sort.Strings(extra)
	return missing, extra
}

// ciStrayDebug looks for dd( and dump( under app/, as the team's CI does
// with grep, but fails when it finds one.
func (a *App) ciStrayDebug() (ciStatus, int) {
	root := filepath.Join(a.Dir, "app")
	if info, err := os.Stat(root); err != nil || !info.IsDir() {
		fmt.Fprintln(a.Stdout, "No app/ folder.")
		return ciSkipped, 0
	}
	found, err := strayDebugCalls(a.Dir, "app")
	if err != nil {
		fmt.Fprintf(a.Stdout, "Couldn't read app/: %v\n", err)
		return ciFailed, 0
	}
	if len(found) == 0 {
		fmt.Fprintln(a.Stdout, "No stray dd() or dump() in app/.")
		return ciPassed, 0
	}
	for _, f := range found {
		fmt.Fprintln(a.Stdout, f)
	}
	fmt.Fprintln(a.Stdout, "Stray dd() or dump() found.")
	return ciFailed, 0
}

// strayDebugCalls returns each line under dir/sub matching strayDebug, as
// path:line: text, with the path relative to dir.
func strayDebugCalls(dir, sub string) ([]string, error) {
	var found []string
	err := filepath.WalkDir(filepath.Join(dir, sub), func(path string, d fs.DirEntry, err error) error {
		if err != nil || !d.Type().IsRegular() {
			return err
		}
		f, err := os.Open(path)
		if err != nil {
			return err
		}
		defer f.Close()
		rel, _ := filepath.Rel(dir, path)
		sc := bufio.NewScanner(f)
		sc.Buffer(make([]byte, 64*1024), 4*1024*1024)
		for n := 1; sc.Scan(); n++ {
			if strayDebug.MatchString(sc.Text()) {
				found = append(found, fmt.Sprintf("%s:%d: %s", filepath.ToSlash(rel), n, strings.TrimSpace(sc.Text())))
			}
		}
		return sc.Err()
	})
	return found, err
}

func (a *App) ciAudit() (ciStatus, int) {
	return a.ciRun(a.inApp("composer", "audit"))
}

func (a *App) ciPint() (ciStatus, int) {
	if !fileExists(filepath.Join(a.Dir, "vendor", "bin", "pint")) {
		fmt.Fprintln(a.Stdout, "Pint isn't installed (no vendor/bin/pint). Migrate to Pint if possible (bothy composer require --dev laravel/pint).")
		return ciFailed, 0
	}
	return a.ciRun(a.inApp("php", "vendor/bin/pint", "--test"))
}

// ciTests makes bothy's test database and user if they aren't there, then
// runs the tests against it, in parallel when paratest is installed.
func (a *App) ciTests() (ciStatus, int) {
	setup := a.inDatabase(ciSetup, true)
	if a.dryRun {
		fmt.Fprintf(a.Stdout, "%s  # makes the %s database and user, if they aren't there\n", shellJoin(append([]string{"docker"}, setup...)), ciDatabase)
	} else if code := a.Pipe(setup, strings.NewReader(ciSetupSQL), a.Stdout); code != 0 {
		fmt.Fprintf(a.Stdout, "Couldn't make the %s test database.\n", ciDatabase)
		return ciFailed, code
	}

	test := []string{"php", "artisan", "test"}
	how := "serially"
	if dirExists(filepath.Join(a.Dir, "vendor", "brianium", "paratest")) {
		test = append(test, "--parallel")
		how = "in parallel"
	}
	fmt.Fprintf(a.Stdout, "On MariaDB, in the %s database (yours isn't touched), %s.\n", ciDatabase, how)
	x := append([]string{"exec"}, a.noTTY()...)
	for _, e := range ciTestEnv {
		x = append(x, "-e", e)
	}
	x = append(x, appService)
	return a.ciRun(compose(append(x, test...)...))
}

// ciRun runs one docker command as a check.
func (a *App) ciRun(args []string) (ciStatus, int) {
	code := a.Run(args)
	if code != 0 {
		return ciFailed, code
	}
	return ciPassed, 0
}

func dirExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.IsDir()
}
