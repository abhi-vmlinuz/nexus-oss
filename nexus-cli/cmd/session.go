package cmd

import (
	"encoding/json"
	"fmt"
	"os"
	"text/tabwriter"
	"time"

	"github.com/nexus-oss/nexus/nexus-cli/client"
	"github.com/spf13/cobra"
)

func newSessionCmd(c *client.Client) *cobra.Command {
	cmd := &cobra.Command{
		Use:     "session",
		Short:   "Manage sessions (create, list, get, terminate, extend)",
		Aliases: []string{"s", "sess"},
	}
	cmd.AddCommand(
		newSessionCreateCmd(c),
		newSessionListCmd(c),
		newSessionGetCmd(c),
		newSessionTerminateCmd(c),
		newSessionExtendCmd(c),
	)
	return cmd
}

func newSessionCreateCmd(c *client.Client) *cobra.Command {
	var vpnIP string

	cmd := &cobra.Command{
		Use:   "create --challenge <id> --user <id>",
		Short: "Spawn a new challenge session",
		Example: `  nexus session create --challenge pwn-101 --user alice
  nexus session create --challenge web-xss --user bob --vpn-ip 10.8.0.5`,
		RunE: func(cmd *cobra.Command, args []string) error {
			challengeID, _ := cmd.Flags().GetString("challenge")
			userID, _ := cmd.Flags().GetString("user")
			if challengeID == "" || userID == "" {
				return fmt.Errorf("--challenge and --user are required")
			}

			fmt.Printf("Creating session: challenge=%s user=%s\n", challengeID, userID)

			sess, err := c.CreateSession(client.CreateSessionRequest{
				ChallengeID: challengeID,
				UserID:      userID,
				VpnIP:       vpnIP,
			})
			if err != nil {
				return fmt.Errorf("create failed: %w", err)
			}

			fmt.Printf("\nSession created\n")
			fmt.Printf("   Session:   %s\n", sess.ID)
			fmt.Printf("   User:      %s\n", sess.UserID)
			fmt.Printf("   Challenge: %s\n", sess.ChallengeID)
			fmt.Printf("   Pod IP:    %s\n", sess.PodIP)
			fmt.Printf("   Status:    %s\n", sess.Status)
			fmt.Printf("   Expires:   %s\n", sess.ExpiresAt.Format(time.RFC3339))

			if len(sess.Services) > 0 {
				fmt.Printf("\nAvailable Services:\n")
				for _, s := range sess.Services {
					fmt.Printf(" - %-10s -> %s:%d\n", s.Name, sess.PodIP, s.Port)
				}
			}
			return nil
		},
	}
	cmd.Flags().String("challenge", "", "Challenge ID (required)")
	cmd.Flags().String("user", "", "User ID (required)")
	cmd.Flags().StringVar(&vpnIP, "vpn-ip", "", "WireGuard VPN IP for this user (required in prod mode)")

	// Register completion for --challenge flag.
	cmd.RegisterFlagCompletionFunc("challenge", func(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		challenges, err := c.ListChallenges()
		if err != nil {
			return nil, cobra.ShellCompDirectiveError
		}
		var ids []string
		for _, ch := range challenges {
			ids = append(ids, ch.ID+"\t"+ch.Name)
		}
		return ids, cobra.ShellCompDirectiveNoFileComp
	})
	return cmd
}

func newSessionListCmd(c *client.Client) *cobra.Command {
	var all bool

	cmd := &cobra.Command{
		Use:     "list",
		Aliases: []string{"ls"},
		Short:   "List sessions (active only by default)",
		RunE: func(cmd *cobra.Command, args []string) error {
			sessions, err := c.AdminSessions()
			if err != nil {
				return err
			}
			if len(sessions) == 0 {
				fmt.Println("No sessions active.")
				return nil
			}

			w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
			fmt.Fprintln(w, "SESSION\tUSER\tCHALLENGE\tPOD IP\tSTATUS\tEXPIRES")
			count := 0
			for _, s := range sessions {
				// Filter: only show active sessions unless --all is passed.
				if !all && (s.Status == "terminated" || s.Status == "expired" || s.Status == "failed") {
					continue
				}
				expires := humanDurationCLI(time.Until(s.ExpiresAt))
				fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\t%s\n",
					s.ID, s.UserID, s.ChallengeID, s.PodIP, s.Status, expires)
				count++
			}
			if count == 0 && !all {
				fmt.Println("No active sessions. Use --all to see terminated ones.")
				return nil
			}
			return w.Flush()
		},
	}
	cmd.Flags().BoolVarP(&all, "all", "a", false, "Show all sessions including terminated/expired")
	return cmd
}

func newSessionGetCmd(c *client.Client) *cobra.Command {
	return &cobra.Command{
		Use:   "get <session-id>",
		Short: "Get full details for a session",
		Args:  cobra.ExactArgs(1),
		ValidArgsFunction: func(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
			if len(args) != 0 {
				return nil, cobra.ShellCompDirectiveNoFileComp
			}
			sessions, err := c.AdminSessions()
			if err != nil {
				return nil, cobra.ShellCompDirectiveError
			}
			var ids []string
			for _, s := range sessions {
				ids = append(ids, s.ID+"\t"+s.UserID+" ("+s.ChallengeID+")")
			}
			return ids, cobra.ShellCompDirectiveNoFileComp
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			sess, err := c.GetSession(args[0])
			if err != nil {
				return err
			}
			enc := json.NewEncoder(os.Stdout)
			enc.SetIndent("", "  ")
			return enc.Encode(sess)
		},
	}
}

func newSessionTerminateCmd(c *client.Client) *cobra.Command {
	return &cobra.Command{
		Use:     "terminate <session-id>",
		Aliases: []string{"delete", "rm"},
		Short:   "Terminate a session and delete its pod",
		Args:    cobra.ExactArgs(1),
		ValidArgsFunction: func(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
			if len(args) != 0 {
				return nil, cobra.ShellCompDirectiveNoFileComp
			}
			sessions, err := c.AdminSessions()
			if err != nil {
				return nil, cobra.ShellCompDirectiveError
			}
			var ids []string
			for _, s := range sessions {
				ids = append(ids, s.ID+"\t"+s.UserID+" ("+s.ChallengeID+")")
			}
			return ids, cobra.ShellCompDirectiveNoFileComp
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := c.TerminateSession(args[0]); err != nil {
				return err
			}
			fmt.Printf("Session %s terminated\n", args[0])
			return nil
		},
	}
}

func newSessionExtendCmd(c *client.Client) *cobra.Command {
	var minutes int

	cmd := &cobra.Command{
		Use:   "extend <session-id>",
		Short: "Extend session TTL",
		Args:  cobra.ExactArgs(1),
		ValidArgsFunction: func(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
			if len(args) != 0 {
				return nil, cobra.ShellCompDirectiveNoFileComp
			}
			sessions, err := c.AdminSessions()
			if err != nil {
				return nil, cobra.ShellCompDirectiveError
			}
			var ids []string
			for _, s := range sessions {
				ids = append(ids, s.ID+"\t"+s.UserID+" ("+s.ChallengeID+")")
			}
			return ids, cobra.ShellCompDirectiveNoFileComp
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			result, err := c.ExtendSession(args[0], minutes)
			if err != nil {
				return err
			}
			fmt.Printf("Session %s extended\n", args[0])
			fmt.Printf("   Old expiry: %v\n", result["old_expires_at"])
			fmt.Printf("   New expiry: %v\n", result["new_expires_at"])
			return nil
		},
	}
	cmd.Flags().IntVar(&minutes, "minutes", 0, "Minutes to add (default: NEXUS_DEFAULT_SESSION_TTL_MINUTES)")
	cmd.Flags().IntVar(&minutes, "duration", 0, "Alias for --minutes")
	return cmd
}

func humanDurationCLI(d time.Duration) string {
	if d < 0 {
		return "expired"
	}
	h := int(d.Hours())
	m := int(d.Minutes()) % 60
	if h > 0 {
		return fmt.Sprintf("%dh%dm", h, m)
	}
	return fmt.Sprintf("%dm", m)
}
