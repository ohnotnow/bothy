---
name: bothy-lando-migrate
description: Move a Laravel app's local development from lando to bothy (plain Docker Compose on the FrankenPHP base image). Use when a developer asks to convert, migrate or move an app off lando, or when an app has a .lando.yml and they want to start using bothy for it.
---

# Moving an app's development from lando to bothy

This converts one app, in the folder you are in, from lando to bothy for
local development. It writes `compose.dev.yml` and a small development-only
Dockerfile from the copies in this skill's `reference/` folder. It works with
the `bothy` skill: read that too.

**It leaves alone everything QA, CI and production use:** the app's own
`Dockerfile`, its CI file and its deploy or stack files. Moving those is a
separate step, done when the app moves servers. There are two exceptions,
both offers in step 3: updating the node version the app's `Dockerfile`
builds its assets with, and fixing a broken check in its CI file.

Work through the steps in order, and talk to the developer as you go. Stop and
ask wherever a step says so; for a question with a few clear answers, use
your question tool if you have one (AskUserQuestion in Claude Code). The
step numbers are for you: don't mention
them to the developer, and before you do anything they haven't seen coming,
say in plain words what it is and why. Never commit: the developer reviews and commits
the result themselves.

## The reference files

Everything in `reference/` goes into the app at the same path:

- `compose.dev.yml`
- `docker/dev.Dockerfile`
- `docker/custom_php.ini`, `docker/ldap.conf`, `docker/recheck_files.ini`
  (the dev Dockerfile copies these in; most apps already have the first two)

Every reference file carries a line saying "From the bothy-lando-migrate
skill, reference set N." Keep that line in the copy you write. Don't add it
to an app's own file that you leave as it is.

Placeholders in `compose.dev.yml`:

- `{{app}}`: the app's name, which is the project folder's name (it becomes
  `<app>.bothy.localhost`, so it must be a valid hostname part: lower case,
  letters, digits and dashes). If `.lando.yml`'s `name:` is different, tell
  the developer the app's address changes from lando's.
- `{{cache}}` and `{{mail}}`: the host names the app uses for redis and the
  mail catcher, so the developer's `.env` works unchanged. Don't take them
  from `.lando.yml`, which can disagree with what the app actually uses (and
  a redis switched on by the laravel recipe's `config: cache: redis` has no
  name there at all). Instead:
  1. Find which variable holds each host in the app's own config:
     `config/database.php` (the `redis` section) and `config/mail.php` (the
     smtp mailer). Laravel versions differ, so read them, don't assume
     `REDIS_HOST` and `MAIL_HOST`. Newer apps keep only their overrides in
     `config/`: if the section isn't there, read the framework's copy in
     `vendor/laravel/framework/config/`.
  2. Take each variable's value from `.env.example`, which the team keeps
     working for a fresh clone (for example `cache`, `mail` or
     `sendmailhog`).
  3. In step 8, tell the developer which lines their own `.env` needs
     (for example `MAIL_HOST=sendmailhog`), since you can't read it.

Copy from `reference/`, not from another converted app: other apps' copies
may be older, or carry settings only that app needs.

## 1. Before changing anything

- Check `git status`. If the working tree has changes, ask whether to go on.
  If they are on the main branch, suggest a branch for the conversion; the
  developer makes it. Any other branch is fine.
- **Ask whether the app's lando database holds anything worth keeping.**
  Usually it is seed data, and `bothy mfs` rebuilds it. Sometimes it is data
  set up by hand to reproduce a bug, or a copy of production. If it is worth
  keeping, suggest `lando db-export` and that the file goes somewhere outside
  the project folder (say why: a dump in the folder can be committed by
  mistake, or baked into the production image). If it came from production,
  say plainly that it holds real people's data.

## 2. Survey the app

- `.lando.yml`: the services and their names, and the tooling. Only services
  that are switched on count: a commented-out service (many apps have a
  commented-out MinIO stub) is not used.
- The PHP version: `.lando.yml`'s `php:`, which is always right. Not the
  app's `Dockerfile` (CI sets its PHP version, so the default written there
  is often stale), not `composer.json` (a floor), and not the Mac's own PHP,
  which a Boost tool may report.
- How the app trusts proxies (step 4). Do not assume it does.

**Stop and ask** if `.lando.yml` has a switched-on service other than the
database, redis, a mail catcher, minio, meilisearch or node.

A `node` service is dropped without asking: npm runs on the Mac under bothy
(`bothy assets` and `bothy watch`), because a Mac's `node_modules` holds
Mac-only binaries a Linux container can't load. The tour in the closing message covers it.

## 3. Write the files

For each file, if the app already has one at that path:

- If it is the same as what you would write, or differs only in the stamp
  line, leave it.
- If it differs otherwise, **show the developer the difference and ask** what
  to do. Never overwrite quietly. A developer may have copied some of these
  files in by hand from another app before they remembered this skill. A
  file with an older "reference set" line came from an earlier version of
  this skill.

The files:

- `docker/dev.Dockerfile`: set `ARG PHP_VERSION` to the app's PHP version.
- `docker/custom_php.ini`, `docker/ldap.conf`, `docker/recheck_files.ini`.
- `compose.dev.yml`, with the placeholders filled in. Each optional service
  sits between marker comments: keep CACHE only if `.lando.yml` has redis
  (and drop the two `# CACHE only` lines under `depends_on` if not), MAIL
  only if it has a mail catcher, MINIO (and `minio:` under `volumes:`) only
  if it has minio, MEILISEARCH only if it has meilisearch. Remove the marker
  comments either way, including the inline `# CACHE only` and
  `# MINIO only` ones on lines you keep.
- `package.json`: `bothy assets` and `bothy watch` run `npm run build`. If
  there is no `build` script (Laravel Mix apps have `dev`, `production`,
  `watch` and so on instead), ask the developer, then add
  `"build": "npm run production"`. Change nothing else in the file.
- The app's own `Dockerfile`, only if the developer agrees. Under bothy,
  npm runs on the developer's Mac, while CI builds the production assets in
  the Dockerfile's node stage (`FROM node:<version>`). Compare the major
  version of the Mac's `node --version` with that line. Compare node, not
  npm: each node image brings its own npm. If the Dockerfile's is two or
  more majors older, show both versions and offer to change the
  `FROM node:` line to the Mac's major. If the line pins a digest
  (`node:22@sha256:...`), keep it pinned: get the new digest with
  `docker buildx imagetools inspect node:<major>`, and bring any comment
  about the pin up to date. Say plainly that this changes the production
  build, which CI will build with. Then one line on whether the app's build
  tool is happy with a modern node, for example "this app uses Vite 8, so a
  modern node is fine". Only if the app uses Laravel Mix older than 6
  (webpack 4), warn instead that node 17 and later fail its build with
  `ERR_OSSL_EVP_UNSUPPORTED`, on the Mac as well as in CI, and that Mix 6 is
  the fix; otherwise don't mention Mix or OpenSSL. Change nothing else in
  the Dockerfile.
- `.gitlab-ci.yml`'s check for stray `dd(` and `dump(` calls. Many of the
  team's apps have it written as `egrep -r '[^a-zA-Z](dd\(|dump\()' app || exit 0`,
  and that line **can never fail**: when grep finds a stray call it exits 0,
  and when it finds none the `|| exit 0` does. A forgotten `dd()` goes to QA
  and production with a green pipeline. If the line is in the middle of a
  job (some apps put it inside `unit-tests`), the `|| exit 0` also ends that
  job early whenever the code is clean, so the lines after it never run.
  This is a real bug, not a style point: don't leave it as a passing remark.
  Explain it to the developer in those plain terms, show them the line, and
  offer to replace it with:

  ```yaml
  - if grep -rnE '[^a-zA-Z](dd|dump)\(' app; then echo "Stray dd() or dump() found"; exit 1; fi
  ```

  Not `! grep ...`: bash's `set -e` ignores a failed command that starts with
  `!`, so it only fails the job as the job's last line. If the line was
  mid-job, the lines after it now run, for the first time in a while: say
  so, since they may fail. If the developer agrees, change that line and
  nothing else in the CI file. If they say no, tell them once more that the
  check is not checking anything, then leave it.
- `.env.example`: set `APP_URL=https://<app>.bothy.localhost`, so that
  `cp .env.example .env` then `bothy start` works for someone cloning the app
  for the first time, as it did with lando. Check the redis and mail host
  values match the service names above. Change nothing else.

## 4. App code: trusted proxies

Laravel must trust bothy's HTTPS proxy, or it builds `http://` links. It
already does if either of these is there:

- `trustProxies(at: '*')` in `bootstrap/app.php`;
- an app-level `TrustProxies` middleware (usually
  `app/Http/Middleware/TrustProxies.php`, swapped in with
  `$middleware->replace(...)` or, in older apps, listed in
  `app/Http/Kernel.php`) whose `$proxies` is `'*'`.

Otherwise add `$middleware->trustProxies(at: '*');` inside `withMiddleware`
(in an older app with `app/Http/Kernel.php`, set `$proxies = '*'` in its
`TrustProxies` middleware instead).

## 5. Agent guidance: lando becomes bothy

The app's agent instructions probably tell agents the team uses lando.

- **Boost.** If the app has `.ai/guidelines/`, look for lando in the blade
  files there (usually a "Use of lando" section in
  `.ai/guidelines/team-conventions.blade.php`). Rewrite that section for
  bothy, keeping the rest of the file as it is, along these lines:

  ```markdown
  ### Use of bothy

  We use bothy for local development: a small wrapper over docker compose
  (see the bothy skill). Run artisan, composer and the tests through it:
  `bothy artisan ...`, `bothy composer ...`, `bothy test`. The Mac may also
  have PHP, but its version may not match the app's, so only use it if the
  developer asks.

  Do not run commands that change or wipe the database unless the developer
  asks. The developer will run migrations for you if you ask.

  Note: The local test environment uses an in-memory database via the
  RefreshDatabase trait. So there is no need to run migrations or seeders in
  the test environment.
  ```

  Keep any lines in the old section that aren't about lando. Lines about
  running commands directly on the Mac are covered by the PHP sentence
  above: don't keep them as well. Boost builds
  `CLAUDE.md` (and `AGENTS.md`) from these files, so they need regenerating
  afterwards: the closing message offers it.
- **CLAUDE.md and AGENTS.md.** A Boost-generated file has section headings
  like `=== .ai/team-conventions rules ===`; regenerating it is the fix, so
  don't edit it by hand. If it lacks a section for a guideline file that
  exists, it was already out of date: warn the developer that regenerating
  will add more than the bothy change. If the file is hand-written and
  mentions lando, show the developer the lines and offer to change them for
  bothy.

## 6. Ignore database dumps

Make sure both `.gitignore` and `.dockerignore` have `*.sql` and `*.sql.gz`,
adding the lines at the end if not (without a second blank line if the file
already ends with one). A dump in the project folder could otherwise be
committed, or baked into the production image by the Dockerfile's
`COPY . /app`. Add nothing else to either file.

Do not touch `.lando.yml`: the developer decides whether to keep it as a
fallback.

## 7. Check, without starting anything

- `docker compose -f compose.dev.yml config -q`.
- `bothy --dry-run start`.
- `docker buildx imagetools inspect uogsoe/soe-php-frankenphp:<PHP_VERSION>`,
  with the version you set in `docker/dev.Dockerfile`: the dry run doesn't
  check that the base image exists for it.

## 8. Hand over to the developer

Tell the developer the files are ready for them to look over, and list the
commands that bring the app up:

1. `lando poweroff`, run from their home folder. lando's proxy holds ports
   80 and 443, which bothy's needs. (From the home folder because, with the
   team's lando settings, any lando command leaves an empty `.ssh/` folder in
   the folder it runs in.)
2. `APP_URL=https://<app>.bothy.localhost` in their `.env`, and the redis
   and mail host lines from step 3 (name the variables and values). They
   edit it themselves.
3. `bothy start`.
4. `bothy composer install`. `vendor/` left over from lando may be stale.
   Never `composer update` here: it rewrites `composer.lock` and upgrades
   the app's dependencies as part of the move.
5. `bothy artisan optimize:clear`, to clear Laravel's cached config, routes,
   views and package lists left over from lando.
6. `bothy assets`, to build the front-end assets fresh.
7. `bothy mfs` for fresh seed data, or `bothy db-import <file>` for the dump
   they saved in step 1. The bothy database starts empty.

Offer to run them. Running stacks and seeding the database is normally the
developer's to start (see the `bothy` skill), so run them only on a clear yes.
If running one is refused, by the developer or by a permission rule, give the
developer the command and leave it there: don't look for another route.

## 9. Offer some checks

Once the app is up (the developer ran the commands above, or you did on their
say-so), ask whether they would like you to check it over, in plain words.
For example: "I can run a few checks now: the tests, that the app's
addresses and ports answer, and that Meilisearch and the S3 storage look
right. Want me to?" (name only the services this app has). It helps on a
developer's first app or two; later they may prefer to look for themselves,
and a "no" is fine.

On a yes, and only then:

- `bothy info`: the addresses and ports.
- `bothy artisan migrate:status`, and `bothy artisan migrate` if any are
  pending.
- `bothy test`.
- If the app has minio: `docker compose -f compose.dev.yml logs minio-bucket`
  says the bucket was made or was there. Don't test the console address with
  curl: its root answers non-browser requests as the S3 API (a 403), while a
  browser is sent on to the console. That is normal, so don't report it.
- If the app has meilisearch: its port from `bothy info` answers
  `curl -s http://127.0.0.1:<port>/health`.

Tell the developer what each check found, in a line each.

## 10. Closing message

Keep it short: a few lines, only what applies to this app. Don't list
bothy's commands here, and don't list anything bothy lacks.

- The app's address, and the mail catcher's.
- If the app had minio: it is RustFS now, an S3 server that keeps the name
  `minio` and the `minioadmin`/`minioadmin` login (`bothy info` shows it).
  Its console is at `https://minio-<app>.bothy.localhost`, which is the way
  to see and download uploads: they are kept in a Docker volume, but in
  RustFS's own format, not as plain files.
- If the app had meilisearch: its index empties whenever the app stops;
  `bothy artisan scout:import "App\Models\<Model>"` fills it again (find the
  searchable models with `rg -l 'use Searchable' app/Models`, and name them).
- If lando ran a different database: it is MariaDB 10.11 now, the database
  the new servers will use. CI still tests against the app's real one.
- If `bothy start` complained about certificates, or you don't know
  whether it did and the developer hasn't opened the app yet:
  `brew install mkcert nss`, then `mkcert -install`, once per Mac.
- If `CLAUDE.md` is Boost-generated and you changed `.ai/guidelines/`: offer
  to run `bothy artisan boost:update` to regenerate it (`boost:install`
  asks questions, so leave that to the developer).
- If `package-lock.json` changed when npm ran on the Mac: worth a look
  before committing.
- `.lando.yml` is still there, for them to keep or bin, and the changed
  files are ready for them to review and commit. Nothing about CI or
  deployment has changed, unless they accepted the node bump (the
  Dockerfile's node line and nothing else) or the `dd` check fix (that one
  line in `.gitlab-ci.yml`).

Then offer a short tour of bothy: "Would you like a quick tour of bothy's
day-to-day commands?" Welcome on a first migration; after a couple, a
developer will say no. On a yes, keep it brief, from the `bothy` skill:
starting and stopping, `info` (the ports change between starts; setting
`BOTHY_FORWARD_DB_PORT` in `.env` pins the database's, for a GUI client),
`artisan`/`tinker`/`test` (`bothy test --parallel` for a faster run),
`ci` (the team's usual CI checks, before a push),
`shell` (or `ssh`) for a shell in the app's container, `mfs`, `assets` and
`watch` (npm runs on the Mac, which is why lando's node service has gone),
`db-export`/`db-import`, and the `permissions.deny` list in bothy's README
(offer it; don't edit their settings). End with `bothy help` for the full
list.
