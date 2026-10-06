package main

import (
	"os"

	"github.com/KalebCole/luma-cli/internal/cli"
)

// Set at release time with -ldflags '-X main.version=<version>'.
var version = "dev"

func main() {
	os.Exit(cli.New(version, os.Stdout, os.Stderr).Run(os.Args[1:]))
}
