# bothy

> **Heads up:** bothy is a very new project, written in a few days for one team's way of working. It runs Docker commands on your machine and sets up a local certificate authority, so read what it does before you trust it, and expect rough edges.

A small command-line helper for running Laravel apps locally with Docker Compose, without needing to remember compose's flags.

## What it does

bothy turns short commands into `docker compose -f compose.dev.yml ...` calls for the app in the current folder.

It also runs one shared [Traefik](https://traefik.io/) proxy, so every app is served at `https://<app>.bothy.localhost` with a locally trusted certificate, and several apps can be up at once.

It is built for a particular setup (Laravel on a FrankenPHP image, MariaDB, a `compose.dev.yml` per app) and doesn't try to be a general-purpose tool.

## Prerequisites

- macOS with [Docker Desktop](https://www.docker.com/products/docker-desktop/) (Linux builds exist but are untested, see below)
- [mkcert](https://github.com/FiloSottile/mkcert), installed with Homebrew (see below)
- An app with a `compose.dev.yml` set up the way bothy expects (see [The compose.dev.yml bothy expects](#the-composedevyml-bothy-expects))
- Go 1.27 or later, only if you build bothy from source

## Getting started

### Install bothy

Download the binary for your machine with curl, make it executable, and put it somewhere on your PATH:

```bash
# Apple Silicon; use bothy-darwin-amd64 on an Intel Mac
curl -L -o bothy https://github.com/ohnotnow/bothy/releases/latest/download/bothy-darwin-arm64
chmod +x bothy
```

The binaries aren't signed. A copy downloaded with a web browser from the [releases page](https://github.com/ohnotnow/bothy/releases) is likely to be blocked by macOS; curl doesn't mark the file that way.

After that, `bothy self-update` fetches and installs new releases (see [Updating](#updating)).

There are Linux builds too (`bothy-linux-amd64`, `bothy-linux-arm64`), but bothy has only been used on macOS so far. On Linux, expect file-ownership problems with the app's project folder: it is mounted into a container that runs as a different user, and that isn't solved yet.

### Build from source

If your Mac won't run the downloaded binary (managed Macs often refuse unsigned ones), build it yourself. You need Go 1.27 or later (`brew install go`).

Clone the repo and run `make install`:

```bash
git clone https://github.com/ohnotnow/bothy.git
cd bothy
make install
```

That puts `bothy` in `~/.local/bin`, the folder the [XDG Base Directory spec](https://specifications.freedesktop.org/basedir-spec/latest/) names for a user's own programs. If that folder isn't on your PATH yet, `make install` prints the line to add to `~/.zshrc`. To install somewhere else, run `make install PREFIX=/somewhere` and it goes in `/somewhere/bin`.

The binary records its version from git and the date it was built, so `bothy version` can tell you when there is a newer release. To update, run `git pull` and `make install` again. `bothy self-update` leaves a binary built this way alone.

`go install github.com/ohnotnow/bothy@latest` also works, and puts `bothy` in `~/go/bin`. A binary built that way says `dev` in `bothy version`, can't tell you about new releases, and is updated by running the same command again.

### Set up mkcert (once per Mac)

bothy serves apps over HTTPS, with a certificate made by mkcert. mkcert creates a certificate authority that only your Mac trusts, and uses it to sign certificates.

```bash
brew install mkcert nss
mkcert -install
```

`nss` lets Firefox trust the certificates as well as Safari and Chrome. `mkcert -install` asks for your password, because it adds the new certificate authority to your keychain. Restart Firefox afterwards if it was open.

> **Never share mkcert's `rootCA-key.pem`.** It lives in the folder that `mkcert -CAROOT` prints, and whoever has it can intercept every secure connection your Mac makes.

After that, bothy makes its own `*.bothy.localhost` certificate the first time you run `bothy start`, and renews it by itself once it has less than 30 days left. Neither step asks for your password.

### Install the Claude Code skill

bothy carries a [Claude Code](https://claude.com/claude-code) skill that tells an agent how to work in an app run by bothy. It's built into the binary, so the skill always matches the bothy you have:

```bash
bothy install-skills
```

That copies it into `~/.claude/skills/bothy/`. It never deletes anything, and asks before replacing a copy you've changed (`d` at the prompt shows the difference). `--dry-run` shows what it would do, `--force` replaces without asking, and `--dir` installs under another folder, e.g. `--dir ~/.config/opencode` for another agent. `bothy version` says whether your installed copy is up to date.

### Start an app

```bash
cd ~/code/myapp
bothy start
```

`start` prints where everything is:

```
app          https://myapp.bothy.localhost
appserver    http://127.0.0.1:54123
database     127.0.0.1:54187  (database myapp, user myapp, password secret)
sendmailhog  http://127.0.0.1:54124
```

Set `APP_URL=https://myapp.bothy.localhost` in the app's `.env` so links built outside a web request (queued mail, artisan commands) point at the right address.

If you still use lando, its proxy holds ports 80 and 443. Run `lando poweroff` first.

## Usage

bothy only runs in a folder with a `compose.dev.yml`. It doesn't look in parent folders.

| Command | What it runs |
| --- | --- |
| `bothy start` | starts the proxy if needed, then `up -d --build`, then shows `info` |
| `bothy stop` | `down` (keeps the database) |
| `bothy rebuild` | `down`, `build --pull` (fresh base images), `up -d`; keeps the database |
| `bothy destroy` | `down -v`, deleting the app's volumes including the database; asks first |
| `bothy info` | the app's address and the ports Docker gave its services |
| `bothy artisan <args>` | `php artisan <args>` in the `appserver` container |
| `bothy tinker` | `php artisan tinker` |
| `bothy composer <args>` | `composer <args>` in `appserver` |
| `bothy test [args]` | `php artisan test [args]` |
| `bothy testf <filter> [args]` | `php artisan test --filter <filter> [args]` |
| `bothy mfs [args]` | `migrate:fresh --seed --seeder=TestDataSeeder [args]`; `--seeder=OtherSeeder` picks another seeder |
| `bothy horizon` | Horizon in the foreground (ctrl-c to stop) |
| `bothy watch [--every N]` | `npm run build` on this machine (not in a container), then again whenever a file under `resources/` changes; checks every second, or every N seconds (e.g. `0.5`); ctrl-c to stop |
| `bothy shell` (or `ssh`) | a bash shell in `appserver` |
| `bothy mysql` (or `database`) | a MariaDB client on the app's database, as root |
| `bothy db-import <file>` | backs up the database, wipes it, then loads a `.sql` or `.sql.gz` dump (see [The database](#the-database)) |
| `bothy db-export [file]` | dumps the database, by default to `<folder>-<date>-<time>.sql.gz`; never overwrites |

These work from any folder:

| Command | What it does |
| --- | --- |
| `bothy ls` | lists running bothy projects |
| `bothy poweroff` | stops every running bothy project and the proxy |
| `bothy volumes` | lists every bothy volume and its size, running or not |
| `bothy version` | prints the version, and says if a newer release is out |
| `bothy self-update` | replaces bothy with the latest release, after asking (see [Updating](#updating)) |
| `bothy install-skills` | installs the Claude Code skill for this bothy (see [Install the Claude Code skill](#install-the-claude-code-skill)) |

Put `--dry-run` before any command to see the docker commands without running them:

```bash
bothy --dry-run mfs
```

### Updating

`bothy self-update` downloads the latest release for your machine, checks it against the release's `SHA256SUMS` file, and swaps it in. It shows the release notes and asks first; `--yes` skips the question, and `--check` only reports, exiting 0 when you're up to date, 1 when a newer release is out and 2 when it couldn't find out.

After the swap it runs the new binary once. If that fails, which is what happens on a Mac that blocks downloaded binaries, it puts the old one back and points you at [Build from source](#build-from-source).

A binary built from source isn't replaced: update it with `git pull` and `make install`.

A new release may carry a changed skill too. After updating, `bothy install-skills --dry-run` shows whether it has.

### The database

To use a GUI database client, `bothy info` shows the port and login. The port is random unless you pin it by setting `BOTHY_FORWARD_DB_PORT` in the app's `.env`, if the app's `compose.dev.yml` uses it as below.

`bothy db-import` checks the dump before it touches anything: it refuses an empty file, and reads a `.gz` all the way through, so a truncated download fails before the wipe. Then it backs up the current database to the system's temp folder and prints the path, so if the import goes wrong, `bothy db-import <that path>` puts it back. If the database is over 1 GB it asks whether to skip the backup, and without a terminal to ask on it refuses to run.

The temp folder clears itself, so copy the backup somewhere else if you want to keep it. On macOS that is `$TMPDIR`, where files are removed after a few days unused. On Linux it is usually `/tmp`, which many systems empty on reboot.

### Addresses

Each app gets one name, `<app>.bothy.localhost`. The certificate is a wildcard, and wildcards only cover one level, so a name like `thing.myapp.bothy.localhost` fails with a certificate error.

## The compose.dev.yml bothy expects

bothy hard-codes a few conventions. An app's `compose.dev.yml` needs:

- a project name ending in `-dev`, e.g. `name: myapp-dev` (this is how `bothy volumes` recognises its own volumes)
- the PHP service named `appserver`
- a MariaDB 10 service named `database`, configured with the `MARIADB_*` environment variables, with its data in a named volume. Without one, MariaDB's image puts it in an anonymous volume, and the next `up` after `bothy stop` starts with an empty database
- three Traefik labels and the external `bothy` network on `appserver`, to be served by the proxy

Trimmed to the parts bothy cares about:

```yaml
name: myapp-dev

services:
  appserver:
    ports:
      - "127.0.0.1::6060"            # Docker picks a free port
    labels:
      - traefik.enable=true
      - traefik.http.routers.myapp.rule=Host(`myapp.bothy.localhost`)
      - traefik.http.services.myapp.loadbalancer.server.port=6060
    networks:
      - default
      - bothy

  database:
    image: mariadb:10.11
    volumes:
      - database:/var/lib/mysql      # named, so stop and rebuild keep the data
    ports:
      - "127.0.0.1:${BOTHY_FORWARD_DB_PORT:-}:3306"

volumes:
  database:

networks:
  bothy:
    external: true                    # created by the bothy proxy
```

If your app's image inherits a healthcheck that only runs every 30 seconds, Traefik won't route to the app until it first reports healthy. Adding `healthcheck: {start_period: 30s, start_interval: 1s, interval: 30s}` to `appserver` fixes that.

## Contributing

Clone the repo (see [Build from source](#build-from-source)), make your change, and run `go vet ./...` and `go test ./...` before opening a pull request.

## Licence

MIT. See [LICENSE](LICENSE).
