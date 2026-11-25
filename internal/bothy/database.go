package bothy

import (
	"compress/gzip"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"
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
	// dbSize prints the database's size on disk in bytes, 0 if it is empty
	// or doesn't exist yet.
	dbSize = asRoot + `mariadb -uroot -N -B -e "SELECT COALESCE(SUM(data_length + index_length), 0) FROM information_schema.tables WHERE table_schema = '$MARIADB_DATABASE'"`
)

// db-import backs up the current database before wiping it, and asks first
// when it is bigger than this on disk. A gzipped dump is much smaller.
const backupAskAbove = 1 << 30

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
	gz := strings.HasSuffix(name, ".gz")
	backup := filepath.Join(a.BackupDir, "bothy-"+filepath.Base(a.Dir)+"-"+a.now().Format("20060102-150405")+".sql.gz")
	wipe, load := a.inDatabase(dbWipe, false), a.inDatabase(dbClient, true)
	if a.dryRun {
		fmt.Fprintf(a.Stdout, "# a backup of the current database first (bothy asks before backing up more than %s):\n", humanBytes(backupAskAbove))
		fmt.Fprintln(a.Stdout, shellJoin(append([]string{"docker"}, a.inDatabase(dbDump, true)...))+" | gzip > "+shellJoin([]string{backup}))
		fmt.Fprintln(a.Stdout, shellJoin(append([]string{"docker"}, wipe...)))
		line := shellJoin(append([]string{"docker"}, load...)) + " < " + shellJoin([]string{name})
		if gz {
			line = "gunzip -c " + shellJoin([]string{name}) + " | " + shellJoin(append([]string{"docker"}, load...))
		}
		fmt.Fprintln(a.Stdout, line)
		return 0
	}

	// Everything that can go wrong with the file, before anything is wiped.
	if code := a.checkDump(name, gz); code != 0 {
		return code
	}
	a.checkDumpIgnored(name)
	if code := a.backupBeforeWipe(backup); code != 0 {
		return code
	}

	f, err := os.Open(a.path(name))
	if err != nil {
		fmt.Fprintf(a.Stderr, "bothy: %v\n", err)
		return 1
	}
	defer f.Close()
	var in io.Reader = f
	if gz {
		zr, err := gzip.NewReader(f)
		if err != nil {
			fmt.Fprintf(a.Stderr, "bothy: %v\n", err)
			return 1
		}
		defer zr.Close()
		in = zr
	}

	fmt.Fprintf(a.Stdout, "Wiping the database and importing %s\n", name)
	code := a.Run(wipe)
	if code == 0 {
		code = a.Pipe(load, in, a.Stdout)
	}
	if code != 0 && fileExists(backup) {
		fmt.Fprintf(a.Stderr, "bothy: the import failed. To put back the database you had: bothy db-import %s\n", shellJoin([]string{backup}))
	}
	return code
}

// checkDump refuses a dump that would wipe the database and then fail or
// load nothing: a missing, empty or non-regular file, or a gzip file that is
// truncated or corrupt, which only shows by reading it to the end.
func (a *App) checkDump(name string, gz bool) int {
	path := a.path(name)
	info, err := os.Stat(path)
	if err != nil {
		fmt.Fprintf(a.Stderr, "bothy: %v\n", err)
		return exitNotFound
	}
	if !info.Mode().IsRegular() {
		fmt.Fprintf(a.Stderr, "bothy: %s is not a file\n", name)
		return 1
	}
	if info.Size() == 0 {
		fmt.Fprintf(a.Stderr, "bothy: %s is empty\n", name)
		return 1
	}
	if !gz {
		return 0
	}
	f, err := os.Open(path)
	if err != nil {
		fmt.Fprintf(a.Stderr, "bothy: %v\n", err)
		return 1
	}
	defer f.Close()
	zr, err := gzip.NewReader(f)
	if err != nil {
		fmt.Fprintf(a.Stderr, "bothy: %s is not a gzip file: %v\n", name, err)
		return 1
	}
	n, err := io.Copy(io.Discard, zr)
	if err != nil {
		fmt.Fprintf(a.Stderr, "bothy: %s is damaged or incomplete: %v\n", name, err)
		return 1
	}
	if n == 0 {
		fmt.Fprintf(a.Stderr, "bothy: %s is empty once unzipped\n", name)
		return 1
	}
	return 0
}

// backupBeforeWipe dumps the current database to backup. Over
// backupAskAbove it asks first, and with no terminal to ask on it refuses:
// a big import should be something a person chose to run.
func (a *App) backupBeforeWipe(backup string) int {
	out, err := a.Output(a.inDatabase(dbSize, true))
	if err != nil {
		fmt.Fprintf(a.Stderr, "bothy: checking the size of the current database: %v\n", err)
		return 1
	}
	size, err := strconv.ParseInt(strings.TrimSpace(string(out)), 10, 64)
	if err != nil {
		fmt.Fprintf(a.Stderr, "bothy: checking the size of the current database: unexpected answer %q\n", out)
		return 1
	}
	if size > backupAskAbove {
		if !a.Interactive {
			fmt.Fprintf(a.Stderr, "bothy: the current database is about %s, so db-import asks before backing it up, and there is no terminal to ask on. Run it from a terminal.\n", humanBytes(size))
			return 1
		}
		if a.askYes(fmt.Sprintf("The current database is about %s, so a backup before the wipe could take a while and use a lot of disk. Skip the backup?", humanBytes(size))) {
			fmt.Fprintln(a.Stdout, "Not backing up.")
			return 0
		}
	}
	fmt.Fprintf(a.Stdout, "Backing up the current database to %s\n", backup)
	// 0o600: on Linux the temp folder is shared with other users, and the
	// database may hold a copy of real data.
	return a.dumpTo(backup, backup)
}

// humanBytes renders a size the way a person would say it, e.g. 1.5 GB.
func humanBytes(n int64) string {
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%d bytes", n)
	}
	div, exp := int64(unit), 0
	for m := n / unit; m >= unit; m /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %cB", float64(n)/float64(div), "KMGTP"[exp])
}

func cmdDbExport(a *App, args []string) int {
	if len(args) > 1 {
		return a.usageError("db-export takes at most one file name")
	}
	name := filepath.Base(a.Dir) + "-" + a.now().Format("20060102-150405") + ".sql.gz"
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

	a.checkDumpIgnored(name)
	if code := a.dumpTo(a.path(name), name); code != 0 {
		return code
	}
	fmt.Fprintf(a.Stdout, "Exported the database to %s\n", name)
	return 0
}

// dumpTo dumps the database to a new file at target, gzipped when its name
// ends in .gz. It never overwrites a file, and removes its own if the dump
// fails. name is how messages refer to the file. Only the developer can read
// it: a dump can hold real data.
func (a *App) dumpTo(target, name string) int {
	f, err := os.OpenFile(target, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if errors.Is(err, fs.ErrExist) {
		fmt.Fprintf(a.Stderr, "bothy: %s already exists, not overwriting it\n", name)
		return 1
	}
	if err != nil {
		fmt.Fprintf(a.Stderr, "bothy: %v\n", err)
		return 1
	}

	code, err := exportTo(a, a.inDatabase(dbDump, true), f, strings.HasSuffix(target, ".gz"))
	if err != nil {
		fmt.Fprintf(a.Stderr, "bothy: writing %s: %v\n", name, err)
		code = max(code, 1)
	}
	if code != 0 {
		os.Remove(target) // don't leave a half-written dump that looks real
	}
	return code
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
