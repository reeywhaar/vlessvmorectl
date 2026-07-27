package cli

import (
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"
	"golang.org/x/term"

	"vlessvmorectl/internal/store"
)

func newUsersCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:     "users",
		Short:   "Manage the administrators who may log in to the panel",
		Aliases: []string{"user"},
		Long: `Manage the administrators who may log in to the panel.

These are panel logins, not VPN users. VPN users live on each vlessvmore node and are
managed from the panel itself (or with ` + "`vlessvmore user`" + ` on the node).

Nobody can log in until at least one administrator exists, and there is no way to
create the first one over the web — that would be a race against whoever reaches the
URL first. A shell on this host is the bootstrap credential.`,
	}
	cmd.AddCommand(newUsersAddCmd(), newUsersListCmd(), newUsersRemoveCmd(), newUsersPasswdCmd())
	return cmd
}

func newUsersAddCmd() *cobra.Command {
	var dataDir string
	var passwordStdin bool

	cmd := &cobra.Command{
		Use:   "add <username> [password]",
		Short: "Create an administrator",
		Long: `Create an administrator.

The password may be given three ways:

    vlessvmorectl users add alice hunter2      as an argument
    vlessvmorectl users add alice              prompted, with echo off
    echo hunter2 | vlessvmorectl users add alice --password-stdin

Passing it as an argument is supported because it is the obvious thing to type, but it
leaves the password in your shell history and briefly visible in ps, so it warns.`,
		Args:         cobra.RangeArgs(1, 2),
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			st, err := store.Open(dataDir)
			if err != nil {
				return err
			}
			password, err := readPassword(cmd, args, passwordStdin, true)
			if err != nil {
				return err
			}
			if _, err := st.Admins.Create(args[0], password, time.Now()); err != nil {
				return err
			}
			// Printed on every mutation so that a `docker run` without -v — where the
			// file lands in the container's ephemeral layer and vanishes — is visible
			// rather than silent.
			fmt.Fprintf(cmd.OutOrStdout(), "created administrator %s\nwrote %s\n", args[0], st.Admins.Path())
			return nil
		},
	}
	f := cmd.Flags()
	f.StringVar(&dataDir, "data-dir", dataDirDefault(), "path to the data directory")
	f.BoolVar(&passwordStdin, "password-stdin", false, "read the password from stdin")
	return cmd
}

func newUsersListCmd() *cobra.Command {
	var dataDir string
	var asJSON bool

	cmd := &cobra.Command{
		Use:          "ls",
		Short:        "List administrators",
		Aliases:      []string{"list"},
		Args:         cobra.NoArgs,
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, _ []string) error {
			st, err := store.Open(dataDir)
			if err != nil {
				return err
			}
			admins := st.Admins.List()

			if asJSON {
				// A dedicated output type with no password_hash field.
				//
				// Marshalling store.Admin directly would print every hash, and the
				// only thing preventing that would be remembering not to. This is the
				// obvious future mistake, so the type makes it impossible instead.
				type row struct {
					Username  string    `json:"username"`
					CreatedAt time.Time `json:"created_at"`
					UpdatedAt time.Time `json:"updated_at"`
				}
				out := make([]row, 0, len(admins))
				for _, a := range admins {
					out = append(out, row{Username: a.Username, CreatedAt: a.CreatedAt, UpdatedAt: a.UpdatedAt})
				}
				return writeJSON(cmd.OutOrStdout(), out)
			}

			if len(admins) == 0 {
				fmt.Fprintln(cmd.ErrOrStderr(),
					"no administrators yet — create one with `vlessvmorectl users add <name>`")
				return nil
			}
			tw := tabwriter.NewWriter(cmd.OutOrStdout(), 0, 0, 2, ' ', 0)
			fmt.Fprintln(tw, "USERNAME\tCREATED\tPASSWORD CHANGED")
			for _, a := range admins {
				fmt.Fprintf(tw, "%s\t%s\t%s\n",
					a.Username,
					a.CreatedAt.Format("2006-01-02"),
					a.UpdatedAt.Format("2006-01-02"))
			}
			return tw.Flush()
		},
	}
	f := cmd.Flags()
	f.StringVar(&dataDir, "data-dir", dataDirDefault(), "path to the data directory")
	f.BoolVar(&asJSON, "json", false, "output JSON")
	return cmd
}

func newUsersRemoveCmd() *cobra.Command {
	var dataDir string
	var yes, force bool

	cmd := &cobra.Command{
		Use:          "rm <username>",
		Short:        "Remove an administrator",
		Aliases:      []string{"remove", "delete"},
		Args:         cobra.ExactArgs(1),
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			st, err := store.Open(dataDir)
			if err != nil {
				return err
			}
			if _, err := st.Admins.Get(args[0]); err != nil {
				return err
			}
			// Emptying the list locks everyone out of a running panel, and the only
			// way back in is a shell on this host. Worth one flag.
			if st.Admins.Count() == 1 && !force {
				return errors.New("refusing to remove the last administrator: nobody would be able to log in. Use --force if that is what you want")
			}
			if !yes && !confirm(cmd, fmt.Sprintf("Remove administrator %s?", args[0])) {
				fmt.Fprintln(cmd.ErrOrStderr(), "cancelled")
				return nil
			}
			if err := st.Admins.Delete(args[0]); err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "removed administrator %s\nwrote %s\n", args[0], st.Admins.Path())
			return nil
		},
	}
	f := cmd.Flags()
	f.StringVar(&dataDir, "data-dir", dataDirDefault(), "path to the data directory")
	f.BoolVarP(&yes, "yes", "y", false, "do not ask for confirmation")
	f.BoolVar(&force, "force", false, "allow removing the last administrator")
	return cmd
}

func newUsersPasswdCmd() *cobra.Command {
	var dataDir string
	var passwordStdin bool

	cmd := &cobra.Command{
		Use:          "passwd <username> [password]",
		Short:        "Change an administrator's password",
		Aliases:      []string{"set-password"},
		Args:         cobra.RangeArgs(1, 2),
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			st, err := store.Open(dataDir)
			if err != nil {
				return err
			}
			password, err := readPassword(cmd, args, passwordStdin, true)
			if err != nil {
				return err
			}
			if _, err := st.Admins.SetPassword(args[0], password, time.Now()); err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "changed the password for %s\nwrote %s\n", args[0], st.Admins.Path())
			// Say it, because otherwise nobody believes it — and the whole point of
			// changing a password after a suspected compromise is that the other
			// person stops being logged in.
			fmt.Fprintf(cmd.ErrOrStderr(), "\n%s is now signed out of the panel everywhere.\n", args[0])
			return nil
		},
	}
	f := cmd.Flags()
	f.StringVar(&dataDir, "data-dir", dataDirDefault(), "path to the data directory")
	f.BoolVar(&passwordStdin, "password-stdin", false, "read the password from stdin")
	return cmd
}

// readPassword resolves the password from an argument, stdin, or an interactive prompt.
func readPassword(cmd *cobra.Command, args []string, fromStdin, confirmTwice bool) (string, error) {
	if fromStdin {
		if len(args) > 1 {
			return "", errors.New("give the password as an argument or with --password-stdin, not both")
		}
		b, err := io.ReadAll(cmd.InOrStdin())
		if err != nil {
			return "", fmt.Errorf("reading password from stdin: %w", err)
		}
		// Trailing newline only: a password may legitimately begin or end with a
		// space, and `echo` adds exactly one "\n".
		return strings.TrimRight(string(b), "\r\n"), nil
	}

	if len(args) > 1 {
		fmt.Fprintln(cmd.ErrOrStderr(),
			"warning: the password is now in your shell history and was briefly visible in ps.\n"+
				"         Prefer `users add <name>` and type it at the prompt, or --password-stdin.")
		return args[1], nil
	}

	fd := int(os.Stdin.Fd())
	if !term.IsTerminal(fd) {
		return "", errors.New("no password given, and stdin is not a terminal to prompt on: pass it as an argument or use --password-stdin")
	}

	fmt.Fprint(cmd.ErrOrStderr(), "Password: ")
	first, err := term.ReadPassword(fd)
	fmt.Fprintln(cmd.ErrOrStderr())
	if err != nil {
		return "", err
	}
	if !confirmTwice {
		return string(first), nil
	}

	fmt.Fprint(cmd.ErrOrStderr(), "Repeat:   ")
	second, err := term.ReadPassword(fd)
	fmt.Fprintln(cmd.ErrOrStderr())
	if err != nil {
		return "", err
	}
	if string(first) != string(second) {
		return "", errors.New("the two passwords did not match")
	}
	return string(first), nil
}
