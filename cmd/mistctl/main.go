// Command mistctl is a read-only, kubectl-style CLI for the Juniper Mist API.
package main

import (
	"os"

	"github.com/tmunzer-AIDE/mistctl/internal/cli"
)

func main() {
	os.Exit(cli.Execute())
}
