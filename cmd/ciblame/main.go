// Command ciblame parses downloaded GitHub Actions log archives into
// per-step waterfalls and diffs step timing across runs, fully offline.
package main

import (
	"os"

	"github.com/JaydenCJ/ciblame/internal/cli"
)

func main() {
	os.Exit(cli.Run(os.Args[1:], os.Stdout, os.Stderr))
}
