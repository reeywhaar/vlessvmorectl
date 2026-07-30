// Package cli implements the vlessvmorectl command line.
//
// Unlike the sibling project, none of these commands talk to the running daemon. There
// is no socket and no client: `users add` opens admins.json, writes it, and exits, and
// the daemon notices on its next request. That works because the daemon never writes
// this file — see store.Admins.ReloadIfChanged — which makes a whole layer of
// machinery unnecessary for the four commands that need it.
package cli

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/spf13/cobra"

	"vlessvmorectl/internal/store"
)

// Execute runs the CLI, applying argv[0] dispatch first.
//
// The image symlinks `users` to the binary, so `docker exec vlessvmorectl users add
// alice` works as well as the canonical spelling.
//
// The check is "does argv[0] name a command", not "is argv[0] something other than the
// program name". The latter is the obvious way to write it and it is wrong: it hijacks
// every binary whose file happens to be called something else — a `go build -o
// vlessvmorectl-dev`, a renamed release artifact, a test harness — and answers every
// invocation with `unknown command "vlessvmorectl-dev"`.
func Execute() int {
	root := NewRootCmd()

	args := os.Args[1:]
	if base := filepath.Base(os.Args[0]); namesACommand(root, base) {
		args = append([]string{base}, args...)
	}

	root.SetArgs(args)
	if err := root.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		return 1
	}
	return 0
}

// NewRootCmd builds the command tree.
func NewRootCmd() *cobra.Command {
	root := &cobra.Command{
		Use:   "vlessvmorectl",
		Short: "A control panel for one or more vlessvmore servers",
		Long: `vlessvmorectl serves a web panel for the vlessvmore nodes you point it at.

It holds no VPN state of its own. The browser talks only to this service, which
forwards each call to the right node with that node's bearer token attached, so no
token ever reaches a laptop and no node has to expose its management API publicly.

Getting started:

    export VLESSVMORE_SERVERS="https://vpn.example.com|<token>"
    vlessvmorectl users add alice        # nobody can log in until you do this
    vlessvmorectl serve                  # listens on :80

Mint a node's token on the node itself:

    docker exec vlessvmore vlessvmore token create panel --raw`,
		SilenceErrors: true,
		// Commands report their own errors; usage text on a runtime failure is noise.
		SilenceUsage: true,
	}

	root.AddCommand(
		newServeCmd(),
		newUsersCmd(),
		newPasskeysCmd(),
		newVersionCmd(),
		newHealthcheckCmd(),
	)
	return root
}

// namesACommand reports whether base is one of the root's subcommands, by name or alias.
func namesACommand(root *cobra.Command, base string) bool {
	for _, cmd := range root.Commands() {
		if cmd.Name() == base || slices.Contains(cmd.Aliases, base) {
			return true
		}
	}
	return false
}

// dataDirDefault resolves the data directory the same way in every command.
func dataDirDefault() string {
	if v := os.Getenv(store.DirEnv); v != "" {
		return v
	}
	return store.DefaultDir
}

// confirm asks a yes/no question. A non-interactive stdin answers no, so a piped
// command cannot accidentally agree to a deletion.
func confirm(cmd *cobra.Command, question string) bool {
	fmt.Fprintf(cmd.ErrOrStderr(), "%s [y/N] ", question)
	var answer string
	if _, err := fmt.Fscanln(cmd.InOrStdin(), &answer); err != nil {
		return false
	}
	answer = strings.ToLower(strings.TrimSpace(answer))
	return answer == "y" || answer == "yes"
}
