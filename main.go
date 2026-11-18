package main

import (
	"io/fs"
	"os"

	"github.com/ohnotnow/bothy/internal/bothy"
)

func main() {
	skills, err := fs.Sub(claudeFiles, "claude")
	if err != nil {
		panic(err)
	}
	os.Exit(bothy.Main(os.Args[1:], skills))
}
