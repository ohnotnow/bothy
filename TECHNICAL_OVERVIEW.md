# Technical Overview

Last updated: 2026-11-25

## What This Is

A small Go command-line tool that turns short commands (`bothy start`, `bothy artisan migrate`) into `docker compose -f compose.dev.yml ...` calls for the Laravel app in the current folder, and runs one shared Traefik proxy so each app is served at `https://<app>.bothy.localhost`.

The README covers what each command does for a user. This file covers how the code is put together.

## Stack

- Go 1.27 (`go.mod` says 1.27.3; releases are built with the latest stable Go)
- One dependency: `github.com/mattn/go-isatty` (terminal detection)
- Standard library only for flags (`flag.FlagSet`), HTTP, gzip and tests
- At run time it shells out to `docker`, and where needed `mkcert`, `npm` and `git`

## Directory Structure

```
main.go                  Entry point: hands the embedded skills to bothy.Main
claudefiles.go           //go:embed claude (must sit at the repo root)
claude/skills/           The agent skills built into the binary
  bothy/                   working in an app run by bothy
  bothy-lando-migrate/     moving an app off lando, with reference/ files it copies
internal/bothy/          All the code, one package
Makefile                 make install: build from a clone, stamped as a source build
.github/workflows/release.yml   Test, build four binaries and publish on a v*.*.* tag
```

## How a Command Runs

`bothy.Main` (`app.go`) builds an `App` and calls `App.Main`, which parses the one global flag (`--dry-run`), looks the command up in the `commands` table and calls its `cmdX(a *App, args []string) int`. The return value is the exit code.

- **The `commands` table in `app.go` is the list of commands.** Each row has the name, usage, help summary, whether it needs a `compose.dev.yml` in the current folder, and the function. Help is printed from it.
- **`App` holds everything that touches the outside world as a field:** `Run`, `Output` and `Pipe` run docker three ways (attached, captured, redirected); `Mkcert`, `Npm` and `Git` run those tools and are nil when one isn't installed; `Now`, `LanAddress` and `TryBinary` are swappable for the same reason. Tests replace them.
- **`--dry-run` swaps `App.Run` for a function that prints the docker command.** Commands that do more than call `Run` check `a.dryRun` themselves and print what they would do.
- **`App.Interactive` is true when stdin and stdout are both terminals.** Without a terminal, `compose exec` gets `-T`, and nothing prompts: a command either carries on with a warning or refuses, depending on what is at stake.

## Conventions Hard-Coded

| What | Value | Where |
| --- | --- | --- |
| Compose file | `compose.dev.yml` in the current folder; parents are not searched | `app.go` |
| App service | `appserver` | `compose.go` |
| Database service | `database`, MariaDB, credentials read from its own `MARIADB_*` environment | `database.go` |
| Project name | ends in `-dev`; the only marker `bothy volumes` has | `volumes.go` |
| Default seeder | `TestDataSeeder` | `compose.go` |
| Address | `*.bothy.localhost`, one level only (wildcard certificate) | `certs.go` |
| Bind address | `BOTHY_BIND`, always set by `start` and `rebuild` | `compose.go` |
| Watched folder | `resources/` | `watch.go` |

## No State

bothy keeps no record of what is running. `ls`, `poweroff`, `info` and `volumes` ask Docker each time and parse its JSON output (`projects.go`, `info.go`, `volumes.go`). A project counts as bothy's when it was started from a `compose.dev.yml`; a volume when its project name ends in `-dev`.

What it does write:

- **Its config folder** (`os.UserConfigDir()/bothy`, on a Mac `~/Library/Application Support/bothy`): `proxy-compose.yml`, `traefik-dynamic.yml` and `certs/`. All three are rewritten or renewed by `start`, so hand edits don't last.
- **The system temp folder:** the backup `db-import` takes before a wipe.
- **`~/.claude/skills/`:** only through `install-skills`.
- **The app's `.gitignore` and `.dockerignore`:** only when `db-export` or `db-import` asks and the developer says yes.

## The Proxy and the Certificate

`start` calls `ensureProxy` (`proxy.go`) before bringing the app up:

1. `ensureCert` (`certs.go`) makes the `*.bothy.localhost` certificate with mkcert if there is none, or a new one when less than 30 days are left.
2. `writeProxyFiles` writes the proxy's compose file (a copy of `internal/bothy/proxy-compose.yml`, built into the binary) and Traefik's settings (a constant in `proxy.go`), and reports whether either changed.
3. If the proxy is running and nothing changed, it is left alone. If something changed it is recreated, because Traefik only reads the certificate at start. If it isn't running, bothy asks Docker which containers hold ports 80 and 443 and refuses if any do (usually lando's proxy).

The Traefik image is pinned by version and digest in `proxy-compose.yml`. That is a file of its own, not a Go string, so that Dependabot's `docker-compose` entry (`.github/dependabot.yml`) can see the image and offer updates. Nothing tests the proxy in CI, so a Traefik update wants a real `bothy start` before it is merged. After a release with a new image, each developer's proxy is recreated on their next `bothy start`.

Apps join the proxy through Traefik labels and the external `bothy` network in their own `compose.dev.yml`. HTTPS and the redirect from HTTP are set on the proxy's entrypoints, so the labels never mention TLS.

## Where Things Live

| File | Purpose |
| --- | --- |
| `app.go` | `App`, the `commands` table, global flag, help, `askYes` |
| `compose.go` | The thin commands: start, stop, rebuild, destroy, artisan, composer, test, testf, mfs, horizon, logs, shell; `--lan`; the cached-config refusal |
| `docker.go` | Running docker for real; finding mkcert |
| `proxy.go`, `certs.go` | The shared Traefik proxy and its certificate |
| `info.go`, `lan.go` | `info`: published ports, routed hostnames, the database and S3 logins, the LAN address picker |
| `database.go` | `mysql`, `db-import` (validate, back up, wipe, load), `db-export` |
| `ignore.go` | The check that a dump in the app folder is ignored by git and `.dockerignore` |
| `ci.go` | `ci`: the five checks, the `bothy_ci` database and user, the summary |
| `watch.go` | `assets` and `watch`: npm on the host, polling `resources/` |
| `projects.go`, `volumes.go` | `ls`, `poweroff`, `volumes` |
| `skills.go` | `install-skills`, and the skills line in `version` |
| `update.go` | `version`, `self-update`, the release lookup and the binary swap |

## Behaviour Worth Knowing Before Changing It

- **`BOTHY_BIND` is set on every `start` and `rebuild`,** to `127.0.0.1` without `--lan`, so a value left in the shell or the app's `.env` can't open an app to the network.
- **`test`, `testf` and `ci` refuse while `bootstrap/cache/config.php` exists.** With a cached config Laravel ignores the tests' database settings and would wipe the developer's database.
- **`ci` runs the tests as a MariaDB user that can only reach `bothy_ci` databases,** passing the connection as environment variables on the `exec`. A test pointed at the real database gets "access denied".
- **`db-import` checks the whole dump before it wipes anything,** reading a `.gz` to the end, then backs up. Over 1 GB it asks, and without a terminal it refuses.
- **`dumpTo` never overwrites, writes with mode 0600, and removes a half-written file.**
- **The ignore check never stops an export or import.** For `.gitignore` it asks git (`check-ignore --no-index`); for `.dockerignore` it only looks for bothy's own two lines.
- **`self-update` refuses dev and source builds, verifies the download against `SHA256SUMS`, renames the new binary into place, runs it once and puts the old one back if that fails.** macOS kills a binary overwritten in place, hence the rename (the Makefile does the same).
- **`install-skills` never deletes and never prompts without a terminal.** An optional skill is only added with `--all`, then kept up to date by plain runs.
- **Terminal detection uses go-isatty,** because `/dev/null` is a character device and fools a plain stat check.
- **Port checks ask Docker,** because macOS refuses an ordinary user a test bind below port 1024.

## Testing

- Framework: the standard `testing` package, nothing else
- Pattern: `fakeApp` (`app_test.go`) builds an `App` in a temp folder with fake `Run`, `Output` and `Mkcert`, and returns the recorded docker calls with the stdout and stderr buffers. Tests call `a.Main([]string{...})` and assert on the docker arguments, the output and the files left behind. No test needs Docker.
- Helpers: `fakeOutput` (answers `docker compose ls` with the proxy running), `withPipe` and `importApp` (`database_test.go`), `fstest.MapFS` standing in for the embedded skills (`skills_test.go`)
- `main_test.go` lists the real embedded skill files, so a stray or missing one fails
- Run: `go vet ./...` and `go test ./...`; `staticcheck ./...` and `govulncheck ./...` if installed

## Build and Release

- `go build -o bothy .` gives a `dev` build: no release check, no self-update.
- `make install` stamps `Version` (from `git describe`), `Built` and `FromSource=yes` through `-ldflags`, and installs to `~/.local/bin`.
- Pushing a `v*.*.*` tag runs `release.yml`: vet, test and govulncheck first, then four binaries (darwin and linux, amd64 and arm64) with `Version` stamped, plus `SHA256SUMS`. Release notes are generated by GitHub.
- `assetName` in `update.go` must stay in step with the build list in `release.yml`.
- Only macOS is tested. The Linux binaries are built and untried.

## Local Development

```bash
go build -o bothy . && ./bothy --dry-run start   # see the docker commands without running them
go vet ./... && go test ./...
make install                                     # replace the installed bothy with this clone's
```

After changing a skill under `claude/`, rebuild and run `bothy install-skills --dry-run` to see what would change in `~/.claude`.
