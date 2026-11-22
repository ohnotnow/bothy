---
name: bothy
description: Working in a Laravel app that runs under bothy (a compose.dev.yml in the project folder, no .lando.yml in use). Use when you need to run artisan, composer, tests or tinker in such an app, or when setting up a developer's machine for one.
---

# bothy

bothy is a small wrapper over `docker compose -f compose.dev.yml`. Each
command is one compose call, apart from `watch`, which runs npm on the Mac;
`bothy --dry-run <command>` prints the exact docker command instead of
running it.

## The rules for agents

- **Never run `bothy start`, `bothy stop`, `bothy rebuild`, `bothy destroy` or
  `bothy poweroff`.** Starting and stopping stacks is the developer's job. If the stack is not running, say so
  and ask them to start it.
- **Never wipe or reset the app's database, by any route.** That means
  `bothy mfs`, `bothy db-import`, and artisan's own `migrate:fresh`,
  `migrate:refresh`, `migrate:reset` and `db:wipe` (through `bothy artisan`
  or any other way in). The developer may be partway through testing or
  demoing a feature with data they set up by hand, or have loaded a real
  dump, and none of these keep a copy. If you think the database needs a
  fresh start, say so and let the developer run it. Plain `bothy artisan
  migrate` (forward migrations only) is fine.
- **Never run `bothy watch`.** It runs `npm run build` on the developer's Mac,
  outside any container, and the app's `package.json` decides what that
  runs. It also runs until stopped.
- **Before installing or updating packages** (npm, composer, uv or any
  other), look for the developer's own guidance on package-manager versions
  and supply-chain settings, such as their global `CLAUDE.md`. This skill
  deliberately has none. If you find none either, say so to the developer
  before going ahead, rather than guessing at settings.
- Run bothy from the app's top folder. It refuses to run anywhere without a
  `compose.dev.yml`, and does not search parent folders.
- Run things inside the stack through bothy, not raw `docker` or
  `docker compose`.
- **Never read, copy or print mkcert's `rootCA-key.pem`** (in the folder
  `mkcert -CAROOT` names). Whoever has it can intercept every secure
  connection this Mac makes. bothy's own certificate files don't need
  touching either; bothy renews them itself.

## Commands you can use

| Command | Runs in the stack |
| --- | --- |
| `bothy artisan <args>` | `php artisan <args>` in `appserver` |
| `bothy composer <args>` | `composer <args>` in `appserver` |
| `bothy test [args]` | `php artisan test [args]` |
| `bothy testf <filter> [args]` | `php artisan test --filter <filter> [args]` |
| `bothy db-export [file]` | dumps the database to a file (default `<folder>-<date>-<time>.sql.gz`; never overwrites) |
| `bothy info` | the app's addresses and ports, which Docker picks at random (read-only) |
| `bothy ls` | lists running bothy projects (read-only) |
| `bothy volumes` | lists every bothy volume and its size, running or not (read-only) |

`tinker`, `shell` (alias `ssh`), `mysql` (alias `database`) and `horizon` are
interactive or run until stopped, so they are for the developer, not for you.

The database commands take the credentials from the database container's own
environment (`MARIADB_ROOT_PASSWORD`, `MARIADB_DATABASE`), so there is no
password to look up. Don't go looking in `compose.dev.yml` or `.env` for one.
(`bothy info` does print the app's database login, for the developer's GUI
client. It is a local development password.)

Without a terminal (as when an agent runs it), bothy adds `-T` to `exec` by
itself. You do not need to.

npm runs on the Mac, not through bothy: run `npm` directly in the app's
folder (but see the package-manager rule above).

## Checklist: a developer's machine

Work through these when helping someone start on a bothy app:

- [ ] Docker Desktop is installed and running (`docker compose version`).
- [ ] `bothy` is on their PATH (`bothy version`).
- [ ] The app has a `compose.dev.yml`, and `bothy --dry-run start` shows the
      command it would run.
- [ ] lando's proxy is not running (`lando poweroff`). It holds ports 80 and
      443, which the shared bothy proxy needs; `bothy start` says so if they
      are taken.
- [ ] mkcert is set up: `brew install mkcert nss`, then `mkcert -install`
      (asks for their password, as it adds a local certificate authority to
      the keychain; restart Firefox afterwards). bothy makes and renews the
      `*.bothy.localhost` certificate itself, with no password needed.
- [ ] `APP_URL` in the app's `.env` is `https://<app>.bothy.localhost`, so
      links built outside a request (queued mail, artisan) are right.
- [ ] **Only loopback by default.** In `compose.dev.yml`, the app server's
      and mail catcher's ports are published on `${BOTHY_BIND:-127.0.0.1}`
      (e.g. `"${BOTHY_BIND:-127.0.0.1}::6060"`), and every other service's on
      `127.0.0.1`. The developer opens the first two to the network with
      `bothy start --lan`, and the next plain `bothy start` closes them
      again. If `bothy info` marks a port "(open to the network)" and the
      developer didn't ask for that, tell them which service and which line.
- [ ] **Deny rules for the commands agents must not run.** bothy's README
      ("Keep agents off the commands that stop stacks or wipe data") has a
      `permissions.deny` list for `~/.claude/settings.json`. Offer it to the
      developer and let them add it themselves; don't edit their settings.
- [ ] **Asset watching.** The team does not use the vite dev server. Instead
      the developer runs `bothy watch`, which runs `npm run build` once, then
      again whenever a file under `resources/` changes (`--every 5` checks
      every five seconds instead of every second). It runs npm on the Mac,
      not in a container, because the Mac's `node_modules` holds Mac-only
      native binaries. Check the developer has `node` and `npm`, and see the
      package-manager rule above.
- [ ] **The Mac's node matches the app's.** Compare the major version from
      `node --version` with the `FROM node:<major>` line in the app's
      `Dockerfile` (the stage that builds the production assets). If they
      differ, tell the developer both versions: their local builds use a
      different node from the one that ships. Leave the fix to them. Don't
      compare npm versions: the image's bundled npm is often older than the
      developer's own guidance asks for.

The app is at `<app>.bothy.localhost`, and its mail catcher, where the app's
`compose.dev.yml` gives it Traefik labels, at `mail-<app>.bothy.localhost`.
`bothy info` lists every address. The certificate is a wildcard, and
wildcards only cover one level, so a name like `mail.app.bothy.localhost`
would fail with a certificate error.
