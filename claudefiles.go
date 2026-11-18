package main

import "embed"

// claudeFiles carries the Claude Code skills that go with this version of
// bothy, built into the binary so `bothy install-skills` can put them in place
// and `bothy version` can tell when the installed copies differ. It lives at
// the repo root because go:embed only reaches files at or below the embedding
// package's directory.
//
//go:embed claude
var claudeFiles embed.FS
