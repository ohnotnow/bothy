# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

## [0.10.0] - 2026-11-25
### Added
- `db-export` and `db-import` check that a dump inside the app folder is ignored by git and by `.dockerignore`. On a terminal they offer to add the missing lines; without one they print a warning for an agent to pass on. The export or import goes ahead either way.
- `TECHNICAL_OVERVIEW.md`, `CLAUDE.md` and this changelog.

### Changed
- The `bothy-lando-migrate` skill writes `**/*.sql` and `**/*.sql.gz` into `.dockerignore`, where a bare `*.sql` only covers the top of the folder.
- The `bothy` skill tells agents to pass the dump warning on to the developer.

## [0.9.0] - 2026-11-25
### Added
- `bothy logs`, which passes its arguments to `docker compose logs`.

## [0.8.0] - 2026-11-25
### Changed
- `install-skills` installs only the `bothy` skill. `--all` adds `bothy-lando-migrate`, which is then kept up to date until its folder is deleted.

## [0.7.0] - 2026-11-25
### Added
- `bothy ci`: the team's usual CI checks run locally (env keys, stray `dd`/`dump`, `composer audit`, Pint, and the tests against MariaDB in a `bothy_ci` database of its own).

### Fixed
- `test` and `testf` refuse to run while Laravel's config is cached, which would have pointed the tests at the developer's own database.

## [0.6.0] - 2026-11-24
### Added
- The `bothy-lando-migrate` skill, which takes an agent through moving an app from lando to bothy.
- `info` shows the console login for a RustFS (S3) service.

### Changed
- The `bothy` skill lets agents start, stop and rebuild stacks when the developer asks, and has them ask before wiping the database.

## [0.5.0] - 2026-11-23
### Added
- `bothy assets`: `npm run build` on the host, once, exiting with npm's exit code.

### Changed
- `info` shows the machine's local network address for a port opened with `--lan`.

## [0.4.1] - 2026-11-22
### Added
- `start --lan` and `rebuild --lan` open the app server and mail catcher to the network. Without the flag bothy sets `BOTHY_BIND=127.0.0.1`.
- `info` marks ports that are open to the network, and `start` warns when one is open without `--lan`.

### Changed
- The `bothy` skill keeps agents off every command that wipes the database, and the README gains a `permissions.deny` example.

### Fixed
- `db-export` creates its file readable only by its owner.
- `poweroff` stops the projects Docker listed even when `COMPOSE_PROJECT_NAME` is set in the shell.

## [0.4.0] - 2026-11-21
### Added
- `bothy watch`: `npm run build` on the host, then again whenever a file under `resources/` changes.

### Changed
- `info` labels each routed address with its service, so a mail catcher at `mail-<app>` no longer shows as a second app.

### Removed
- `bothy npm`. Run npm directly in the app folder.

## [0.3.0] - 2026-11-18
### Added
- The agent skill is built into the binary. `bothy install-skills` installs it, and `bothy version` says whether the installed copy is up to date.

### Changed
- `mfs` runs `migrate:fresh --seed --seeder=TestDataSeeder` in one call, and `--seeder` picks another seeder.

## [0.2.0] - 2026-11-18
### Added
- `bothy self-update`, which checks the download against `SHA256SUMS` and puts the old binary back if the new one won't run.
- `bothy version` says when a newer release is out.
- `make install`, for building from a clone.

## [0.1.1] - 2026-11-17
### Changed
- `db-import` checks the dump and backs up the current database before wiping it, and asks first when the database is over 1 GB.

## [0.1.0] - 2026-11-17
### Added
- First release: `start`, `stop`, `rebuild`, `destroy`, `artisan`, `tinker`, `composer`, `test`, `testf`, `mfs`, `horizon`, `npm`, `shell`, `mysql`, `db-import`, `db-export`, `info`, `ls`, `poweroff`, `volumes` and `--dry-run`.
- A shared Traefik proxy serving each app at `https://<app>.bothy.localhost`, with a mkcert certificate that renews itself.
- An agent skill for working in an app run by bothy.

[Unreleased]: https://github.com/ohnotnow/bothy/compare/v0.10.0...HEAD
[0.10.0]: https://github.com/ohnotnow/bothy/compare/v0.9.0...v0.10.0
[0.9.0]: https://github.com/ohnotnow/bothy/compare/v0.8.0...v0.9.0
[0.8.0]: https://github.com/ohnotnow/bothy/compare/v0.7.0...v0.8.0
[0.7.0]: https://github.com/ohnotnow/bothy/compare/v0.6.0...v0.7.0
[0.6.0]: https://github.com/ohnotnow/bothy/compare/v0.5.0...v0.6.0
[0.5.0]: https://github.com/ohnotnow/bothy/compare/v0.4.1...v0.5.0
[0.4.1]: https://github.com/ohnotnow/bothy/compare/v0.4.0...v0.4.1
[0.4.0]: https://github.com/ohnotnow/bothy/compare/v0.3.0...v0.4.0
[0.3.0]: https://github.com/ohnotnow/bothy/compare/v0.2.0...v0.3.0
[0.2.0]: https://github.com/ohnotnow/bothy/compare/v0.1.1...v0.2.0
[0.1.1]: https://github.com/ohnotnow/bothy/compare/v0.1.0...v0.1.1
[0.1.0]: https://github.com/ohnotnow/bothy/releases/tag/v0.1.0
