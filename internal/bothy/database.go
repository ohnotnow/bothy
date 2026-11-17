package bothy

import (
	"compress/gzip"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// The database commands run the MariaDB tools inside the database container
// and let its shell fill in the credentials from the container's own
// environment, so bothy never sees a password. MariaDB 10 is hard-coded.
const (
	dbService = "database"
	asRoot    = `MYSQL_PWD="$MARIADB_ROOT_PASSWORD" exec `
	dbClient  = asRoot + `mariadb -uroot "$MARIADB_DATABASE"`
	dbDump    = asRoot + `mariadb-dump -uroot --single-transaction "$MARIADB_DATABASE"`
	dbWipe    = asRoot + "mariadb -uroot -e \"DROP DATABASE IF EXISTS \\`$MARIADB_DATABASE\\`; CREATE DATABASE \\`$MARIADB_DATABASE\\`;\""
)

// inDatabase builds `compose exec [-T] database sh -c <script>`. Pass
// piped=true when stdin or stdout is redirected, which always needs -T.
func (a *App) inDatabase(script string, piped bool) []string {
	x := []string{"exec"}
	if piped || !a.Interactive {
		x = append(x, "-T")
	}
	return compose(append(x, dbService, "sh", "-c", script)...)
}

// path resolves a user-given file name against the app folder.
func (a *App) path(name string) string {
	if filepath.IsAbs(name) {
		return name
	}
	return filepath.Join(a.Dir, name)
}

func cmdMysql(a *App, args []string) int {
	if len(args) > 0 {
		return a.usageError("mysql takes no arguments")
	}
	return a.Run(a.inDatabase(dbClient, false))
}

func cmdDbImport(a *App, args []string) int {
	if len(args) != 1 {
		return a.usageError("db-import needs one file, e.g. bothy db-import dump.sql.gz")
	}
	name := args[0]
	wipe, load := a.inDatabase(dbWipe, false), a.inDatabase(dbClient, true)
	if a.dryRun {
		fmt.Fprintln(a.Stdout, shellJoin(append([]string{"docker"}, wipe...)))
		line := shellJoin(append([]string{"docker"}, load...)) + " < " + shellJoin([]string{name})
		if strings.HasSuffix(name, ".gz") {
			line = "gunzip -c " + shellJoin([]string{name}) + " | " + shellJoin(append([]string{"docker"}, load...))
		}
		fmt.Fprintln(a.Stdout, line)
		return 0
	}

	f, err := os.Open(a.path(name))
	if err != nil {
		fmt.Fprintf(a.Stderr, "bothy: %v\n", err)
		return exitNotFound
	}
	defer f.Close()
	var in io.Reader = f
	if strings.HasSuffix(name, ".gz") {
		gz, err := gzip.NewReader(f)
		if err != nil {
			fmt.Fprintf(a.Stderr, "bothy: %s is not a gzip file: %v\n", name, err)
			return 1
		}
		defer gz.Close()
		in = gz
	}

	fmt.Fprintf(a.Stdout, "Wiping the database and importing %s\n", name)
	if code := a.Run(wipe); code != 0 {
		return code
	}
	return a.Pipe(load, in, a.Stdout)
}

func cmdDbExport(a *App, args []string) int {
	if len(args) > 1 {
		return a.usageError("db-export takes at most one file name")
	}
	name := filepath.Base(a.Dir) + "-" + time.Now().Format("20060102-150405") + ".sql.gz"
	if len(args) == 1 {
		name = args[0]
	}
	dump := a.inDatabase(dbDump, true)
	if a.dryRun {
		pipe := " > "
		if strings.HasSuffix(name, ".gz") {
			pipe = " | gzip > "
		}
		fmt.Fprintln(a.Stdout, shellJoin(append([]string{"docker"}, dump...))+pipe+shellJoin([]string{name}))
		return 0
	}

	target := a.path(name)
	f, err := os.OpenFile(target, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
	if errors.Is(err, fs.ErrExist) {
		fmt.Fprintf(a.Stderr, "bothy: %s already exists, not overwriting it\n", name)
		return 1
	}
	if err != nil {
		fmt.Fprintf(a.Stderr, "bothy: %v\n", err)
		return 1
	}

	code, err := exportTo(a, dump, f, strings.HasSuffix(name, ".gz"))
	if err != nil {
		fmt.Fprintf(a.Stderr, "bothy: writing %s: %v\n", name, err)
		code = max(code, 1)
	}
	if code != 0 {
		os.Remove(target) // don't leave a half-written dump that looks real
		return code
	}
	fmt.Fprintf(a.Stdout, "Exported the database to %s\n", name)
	return 0
}

// exportTo runs the dump into f, gzipping when asked, and closes f.
func exportTo(a *App, dump []string, f *os.File, gz bool) (int, error) {
	var out io.Writer = f
	var zw *gzip.Writer
	if gz {
		zw = gzip.NewWriter(f)
		out = zw
	}
	code := a.Pipe(dump, nil, out)
	var err error
	if zw != nil {
		err = zw.Close()
	}
	return code, errors.Join(err, f.Close())
}
