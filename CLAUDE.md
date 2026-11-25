# bothy

A small Go command-line helper that takes lando's place for one team's Laravel apps. Each command is a thin translation into `docker compose -f compose.dev.yml ...`, and one shared Traefik proxy serves every app at `https://<app>.bothy.localhost`.

## Where to read

- [README.md](README.md): what each command does, how to install bothy, and the `compose.dev.yml` it expects.
- [TECHNICAL_OVERVIEW.md](TECHNICAL_OVERVIEW.md): how the code is put together, and behaviour worth knowing before changing it.
- [CHANGELOG.md](CHANGELOG.md): what changed in each release.
- `claude/skills/`: the agent skills built into the binary.

A command's behaviour is described in the README, the `bothy` skill and the changelog as well as the code, so a change to one usually touches the others.

## The spirit of it

A bothy is a plain shelter with no frills. These are where the design starts, not laws.

- **The compose files are the source of truth** for how an app and its dev setup run. bothy translates commands and holds no settings of its own.
- **bothy keeps no state.** What is running, on which ports, with which volumes: it asks Docker each time, so its picture can't drift.
- **No config file for now.** The commands are built in, and personal shortcuts are shell aliases.
- **It stays small.** It serves one team's apps and their conventions and is not a general lando replacement. A small allowance for other setups, such as a single flag, is fine; anything bigger belongs in a fork.
- **bothy never rewrites an app.** Moving an app off lando is an agent's job, guided by the `bothy-lando-migrate` skill.
- **The skills nudge agents; the developer decides.** bothy is not a containment boundary by itself.
