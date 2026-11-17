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
- Go 1.27 or later, only if you install with `go install` or want to work on bothy itself

## Getting started

### Install bothy

Download the binary for your machine with curl, make it executable, and put it somewhere on your PATH:

```bash
# Apple Silicon; use bothy-darwin-amd64 on an Intel Mac
curl -L -o bothy https://github.com/ohnotnow/bothy/releases/latest/download/bothy-darwin-arm64
chmod +x bothy
```

The binaries aren't signed. A copy downloaded with a web browser from the [releases page](https://github.com/ohnotnow/bothy/releases) is likely to be blocked by macOS; curl doesn't mark the file that way.

There are Linux builds too (`bothy-linux-amd64`, `bothy-linux-arm64`), but bothy has only been used on macOS so far. On Linux, expect file-ownership problems with the app's project folder: it is mounted into a container that runs as a different user, and that isn't solved yet.

Or, with Go installed:

```bash
go install github.com/ohnotnow/bothy@latest
```

### Set up mkcert (once per Mac)

bothy serves apps over HTTPS, with a certificate made by mkcert. mkcert creates a certificate authority that only your Mac trusts, and uses it to sign certificates.

```bash
brew install mkcert nss
mkcert -install
```

`nss` lets Firefox trust the certificates as well as Safari and Chrome. `mkcert -install` asks for your password, because it adds the new certificate authority to your keychain. Restart Firefox afterwards if it was open.

> **Never share mkcert's `rootCA-key.pem`.** It lives in the folder that `mkcert -CAROOT` prints, and whoever has it can intercept every secure connection your Mac makes.

After that, bothy makes its own `*.bothy.localhost` certificate the first time you run `bothy start`, and renews it by itself once it has less than 30 days left. Neither step asks for your password.

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
| `bothy mfs` | `migrate:fresh`, then `db:seed --class=TestDataSeeder` |
| `bothy horizon` | Horizon in the foreground (ctrl-c to stop) |
| `bothy npm <args>` | `npm <args>` in a throwaway `node` container |
| `bothy shell` (or `ssh`) | a bash shell in `appserver` |
| `bothy mysql` (or `database`) | a MariaDB client on the app's database, as root |
| `bothy db-import <file>` | wipes the database, then loads a `.sql` or `.sql.gz` dump |
| `bothy db-export [file]` | dumps the database, by default to `<folder>-<date>-<time>.sql.gz`; never overwrites |

These work from any folder:

| Command | What it does |
| --- | --- |
| `bothy ls` | lists running bothy projects |
| `bothy poweroff` | stops every running bothy project and the proxy |
| `bothy volumes` | lists every bothy volume and its size, running or not |
| `bothy version` | prints the version |

Put `--dry-run` before any command to see the docker commands without running them:

```bash
bothy --dry-run mfs
```

### The database

To use a GUI database client, `bothy info` shows the port and login. The port is random unless you pin it by setting `BOTHY_FORWARD_DB_PORT` in the app's `.env`, if the app's `compose.dev.yml` uses it as below.

### Addresses

Each app gets one name, `<app>.bothy.localhost`. The certificate is a wildcard, and wildcards only cover one level, so a name like `thing.myapp.bothy.localhost` fails with a certificate error.

## The compose.dev.yml bothy expects

bothy hard-codes a few conventions. An app's `compose.dev.yml` needs:

- a project name ending in `-dev`, e.g. `name: myapp-dev` (this is how `bothy volumes` recognises its own volumes)
- the PHP service named `appserver`
- a MariaDB 10 service named `database`, configured with the `MARIADB_*` environment variables
- a `node` service, if you want `bothy npm`
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
    ports:
      - "127.0.0.1:${BOTHY_FORWARD_DB_PORT:-}:3306"

  node:
    profiles: ["tools"]               # not started by `up`

networks:
  bothy:
    external: true                    # created by the bothy proxy
```

If your app's image inherits a healthcheck that only runs every 30 seconds, Traefik won't route to the app until it first reports healthy. Adding `healthcheck: {start_period: 30s, start_interval: 1s, interval: 30s}` to `appserver` fixes that.

## Contributing

Clone the repo, make your change, and run `go vet ./...` and `go test ./...` before opening a pull request.

```bash
git clone https://github.com/ohnotnow/bothy.git
cd bothy
go build -o bothy .
```

## Licence

MIT. See [LICENSE](LICENSE).
