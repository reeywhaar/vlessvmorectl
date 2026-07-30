package cli

import (
	"fmt"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"

	"vlessvmorectl/internal/store"
)

// Read-only, and one command only.
//
// There is deliberately no `passkeys rm`. passkeys.json is rewritten wholesale from the
// daemon's memory, so a second writer's edit would be silently undone by the next save — the
// mistake store.ErrReadOnly exists to prevent. Removing a passkey is done from the account
// page, where the "last used" date tells the operator *which* device they are removing;
// getting in to do that needs only the password, which is never taken away. And revoking
// somebody else entirely is `users rm`, which the daemon then tidies up after.
func newPasskeysCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "passkeys",
		Short: "Inspect the passkeys administrators have enrolled",
		Long: `Inspect the passkeys administrators have enrolled.

Read-only. A passkey is added and removed from the panel's own account page; this is here so
an operator with a shell can see what exists without one.

Passkeys are only offered when VLESSVMORE_PASSKEY_ORIGIN is set. Credentials already enrolled
survive that variable being removed, but nobody can use them until it comes back.`,
		Aliases: []string{"passkey"},
	}
	cmd.AddCommand(newPasskeysListCmd())
	return cmd
}

func newPasskeysListCmd() *cobra.Command {
	var dataDir string
	var asJSON bool

	cmd := &cobra.Command{
		Use:          "ls",
		Short:        "List enrolled passkeys",
		Aliases:      []string{"list"},
		Args:         cobra.NoArgs,
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, _ []string) error {
			st, err := store.Open(dataDir)
			if err != nil {
				return err
			}

			type row struct {
				Username   string     `json:"username"`
				ID         string     `json:"id"`
				Label      string     `json:"label"`
				Algorithm  int        `json:"algorithm"`
				Synced     bool       `json:"synced"`
				CreatedAt  time.Time  `json:"created_at"`
				LastUsedAt *time.Time `json:"last_used_at,omitempty"`
			}

			// Resolved here rather than stored: passkeys.json names the permanent admin id,
			// so a rename needs no rewrite of this file.
			rows := make([]row, 0)
			for _, owner := range st.Passkeys.Owners() {
				username := "(deleted)"
				if admin, err := st.Admins.GetByID(owner.AdminID); err == nil {
					username = admin.Username
				}
				for _, c := range owner.Credentials {
					r := row{
						Username:  username,
						ID:        c.ID,
						Label:     c.Label,
						Algorithm: c.Algorithm,
						Synced:    c.BackupState,
						CreatedAt: c.CreatedAt,
					}
					if !c.LastUsedAt.IsZero() {
						used := c.LastUsedAt
						r.LastUsedAt = &used
					}
					rows = append(rows, r)
				}
			}

			if asJSON {
				// A dedicated type with no public_key and no credential_id, for the same
				// reason `users ls` has no password_hash: the only thing stopping a leak
				// should not be remembering not to.
				return writeJSON(cmd.OutOrStdout(), rows)
			}

			if len(rows) == 0 {
				fmt.Fprintln(cmd.ErrOrStderr(),
					"no passkeys enrolled — an administrator adds one from the panel's account page")
				return nil
			}
			tw := tabwriter.NewWriter(cmd.OutOrStdout(), 0, 0, 2, ' ', 0)
			fmt.Fprintln(tw, "USERNAME\tNAME\tWHERE\tADDED\tLAST USED")
			for _, r := range rows {
				where := "this device"
				if r.Synced {
					where = "synced"
				}
				used := "never"
				if r.LastUsedAt != nil {
					used = r.LastUsedAt.Format("2006-01-02")
				}
				fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\n",
					r.Username, r.Label, where, r.CreatedAt.Format("2006-01-02"), used)
			}
			return tw.Flush()
		},
	}
	f := cmd.Flags()
	f.StringVar(&dataDir, "data-dir", dataDirDefault(), "path to the data directory")
	f.BoolVar(&asJSON, "json", false, "output JSON")
	return cmd
}
