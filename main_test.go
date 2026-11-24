package main

import (
	"io/fs"
	"reflect"
	"testing"
)

// The built-in skills are exactly these files, so a stray or missing one
// fails loudly.
func TestBuiltInSkills(t *testing.T) {
	var got []string
	err := fs.WalkDir(claudeFiles, "claude", func(path string, d fs.DirEntry, err error) error {
		if err == nil && !d.IsDir() {
			got = append(got, path)
		}
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	want := []string{
		"claude/skills/bothy/SKILL.md",
		"claude/skills/bothy-lando-migrate/SKILL.md",
		"claude/skills/bothy-lando-migrate/reference/compose.dev.yml",
		"claude/skills/bothy-lando-migrate/reference/docker/custom_php.ini",
		"claude/skills/bothy-lando-migrate/reference/docker/dev.Dockerfile",
		"claude/skills/bothy-lando-migrate/reference/docker/ldap.conf",
		"claude/skills/bothy-lando-migrate/reference/docker/recheck_files.ini",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("built in %v, want %v", got, want)
	}
}
