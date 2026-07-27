// Command vlessvmorectl serves a control panel for one or more vlessvmore servers.
package main

import (
	"os"

	"vlessvmorectl/internal/cli"
)

func main() { os.Exit(cli.Execute()) }
