// Command vlessvmorectl serves a control panel for one or more vlessvmore servers.
package main

import (
	"os"
	"time"

	"vlessvmorectl/internal/cli"
)

func main() {
	// Every time this prints is UTC, whatever TZ says. The image carries no zone database, so
	// a named TZ would resolve to UTC regardless — pinning it makes that a decision, not a
	// side effect of what the runtime happens to contain.
	time.Local = time.UTC
	os.Exit(cli.Execute())
}
