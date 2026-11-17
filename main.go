package main

import (
	"os"

	"github.com/ohnotnow/bothy/internal/bothy"
)

func main() {
	os.Exit(bothy.Main(os.Args[1:]))
}
