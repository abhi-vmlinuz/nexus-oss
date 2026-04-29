package cmd

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"text/tabwriter"

	"github.com/nexus-oss/nexus/nexus-cli/client"
	"github.com/spf13/cobra"
)

func newChallengeCmd(c *client.Client) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "challenge",
		Short: "Manage challenges (register, list, delete, rebuild)",
		Aliases: []string{"ch", "chal"},
	}
	cmd.AddCommand(
		newChallengeRegisterCmd(c),
		newChallengeListCmd(c),
		newChallengeGetCmd(c),
		newChallengeDeleteCmd(c),
		newChallengeRebuildCmd(c),
	)
	return cmd
}

func newChallengeRegisterCmd(c *client.Client) *cobra.Command {
	var ttl int
	var ports []int

	cmd := &cobra.Command{
		Use:   "register --name <name> (--dockerfile <path> | --compose <path>)",
		Short: "Register a challenge (single-container or multi-container via Compose)",
		Example: `  nexus challenge register --name pwn-101 --dockerfile ./pwn-101/Dockerfile
  nexus challenge register --name hard-pwn --compose ./hard-pwn/docker-compose.yml`,
		RunE: func(cmd *cobra.Command, args []string) error {
			name, _ := cmd.Flags().GetString("name")
			dockerfile, _ := cmd.Flags().GetString("dockerfile")
			composePath, _ := cmd.Flags().GetString("compose")

			if name == "" {
				return fmt.Errorf("--name is required")
			}
			if dockerfile == "" && composePath == "" {
				return fmt.Errorf("one of --dockerfile or --compose is required")
			}
			if dockerfile != "" && composePath != "" {
				return fmt.Errorf("use either --dockerfile or --compose, not both")
			}

			var req client.RegisterChallengeRequest

			if composePath != "" {
				// ── Multi-container: send compose path to engine for server-side build ──
				absCompose, err := filepath.Abs(composePath)
				if err != nil {
					return fmt.Errorf("cannot resolve compose path %q: %w", composePath, err)
				}
				if _, err := os.Stat(absCompose); os.IsNotExist(err) {
					return fmt.Errorf("compose file not found: %s", absCompose)
				}
				fmt.Printf("🐳 Registering multi-container challenge %q...\n", name)
				fmt.Println("   (Building images on engine, this may take a minute)")
				req = client.RegisterChallengeRequest{
					Name:        name,
					ComposePath: absCompose,
					TTLMinutes:  ttl,
				}
			} else {
				// ── Single-container: build via engine ────────────────────────────────
				absDockerfile, err := filepath.Abs(dockerfile)
				if err != nil {
					return fmt.Errorf("cannot resolve dockerfile path %q: %w", dockerfile, err)
				}
				if _, err := os.Stat(absDockerfile); os.IsNotExist(err) {
					return fmt.Errorf("dockerfile not found: %s", absDockerfile)
				}
				fmt.Printf("🔨 Building image for challenge %q from %s\n", name, absDockerfile)
				fmt.Println("   This may take a minute…")
				req = client.RegisterChallengeRequest{
					Name:           name,
					DockerfilePath: absDockerfile,
					TTLMinutes:     ttl,
					Ports:          ports,
				}
			}

			ch, err := c.RegisterChallenge(req)
			if err != nil {
				return fmt.Errorf("register failed: %w", err)
			}

			fmt.Printf("\n✅ Challenge registered\n")
			fmt.Printf("   ID:    %s\n", ch.ID)
			fmt.Printf("   TTL:   %dm\n", ch.TTLMinutes)
			if len(ch.Containers) > 0 {
				fmt.Printf("   Type:  multi-container (%d services)\n", len(ch.Containers))
				for _, ct := range ch.Containers {
					fmt.Printf("   · %-15s %s  ports: %s\n", ct.Name, ct.Image, formatInts(ct.Ports))
				}
				fmt.Printf("   Ports: %s (combined)\n", formatInts(ch.Ports))
			} else {
				fmt.Printf("   Type:  single-container\n")
				fmt.Printf("   Image: %s\n", ch.Image)
				fmt.Printf("   Ports: %s\n", formatInts(ch.Ports))
			}
			return nil
		},
	}
	cmd.Flags().String("name", "", "Challenge name (required)")
	cmd.Flags().String("dockerfile", "", "Path to Dockerfile (single-container)")
	cmd.Flags().String("compose", "", "Path to docker-compose.yml (multi-container)")
	cmd.Flags().IntVar(&ttl, "ttl", 0, "Session TTL in minutes")
	cmd.Flags().IntSliceVar(&ports, "ports", nil, "Exposed ports, overrides EXPOSE (single-container only)")
	return cmd
}

func newChallengeListCmd(c *client.Client) *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List all registered challenges",
		RunE: func(cmd *cobra.Command, args []string) error {
			challenges, err := c.ListChallenges()
			if err != nil {
				return err
			}
			if len(challenges) == 0 {
				fmt.Println("No challenges registered.")
				return nil
			}
			w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
			fmt.Fprintln(w, "ID\tNAME\tTYPE\tTTL\tPORTS\tIMAGE")
			for _, ch := range challenges {
				challengeType := "single"
				image := ch.Image
				if len(ch.Containers) > 0 {
					challengeType = fmt.Sprintf("multi(%d)", len(ch.Containers))
					image = "—"
				}
				fmt.Fprintf(w, "%s\t%s\t%s\t%dm\t%s\t%s\n",
					ch.ID, ch.Name, challengeType, ch.TTLMinutes, formatInts(ch.Ports), image)
			}
			return w.Flush()
		},
	}
}

func newChallengeGetCmd(c *client.Client) *cobra.Command {
	return &cobra.Command{
		Use:   "get <challenge-id>",
		Short: "Get details for a single challenge",
		Args:  cobra.ExactArgs(1),
		ValidArgsFunction: func(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
			if len(args) != 0 {
				return nil, cobra.ShellCompDirectiveNoFileComp
			}
			challenges, err := c.ListChallenges()
			if err != nil {
				return nil, cobra.ShellCompDirectiveError
			}
			var ids []string
			for _, ch := range challenges {
				ids = append(ids, ch.ID+"\t"+ch.Name)
			}
			return ids, cobra.ShellCompDirectiveNoFileComp
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			ch, err := c.GetChallenge(args[0])
			if err != nil {
				return err
			}
			enc := json.NewEncoder(os.Stdout)
			enc.SetIndent("", "  ")
			return enc.Encode(ch)
		},
	}
}

func newChallengeDeleteCmd(c *client.Client) *cobra.Command {
	return &cobra.Command{
		Use:   "delete <challenge-id>",
		Short: "Delete a challenge (does not terminate existing sessions)",
		Args:  cobra.ExactArgs(1),
		ValidArgsFunction: func(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
			if len(args) != 0 {
				return nil, cobra.ShellCompDirectiveNoFileComp
			}
			challenges, err := c.ListChallenges()
			if err != nil {
				return nil, cobra.ShellCompDirectiveError
			}
			var ids []string
			for _, ch := range challenges {
				ids = append(ids, ch.ID+"\t"+ch.Name)
			}
			return ids, cobra.ShellCompDirectiveNoFileComp
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := c.DeleteChallenge(args[0]); err != nil {
				return err
			}
			fmt.Printf("✅ Challenge %s deleted\n", args[0])
			return nil
		},
	}
}

func newChallengeRebuildCmd(c *client.Client) *cobra.Command {
	return &cobra.Command{
		Use:   "rebuild <challenge-id>",
		Short: "Rebuild the Docker image for a registered challenge",
		Args:  cobra.ExactArgs(1),
		ValidArgsFunction: func(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
			if len(args) != 0 {
				return nil, cobra.ShellCompDirectiveNoFileComp
			}
			challenges, err := c.ListChallenges()
			if err != nil {
				return nil, cobra.ShellCompDirectiveError
			}
			var ids []string
			for _, ch := range challenges {
				ids = append(ids, ch.ID+"\t"+ch.Name)
			}
			return ids, cobra.ShellCompDirectiveNoFileComp
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			fmt.Printf("🔨 Rebuilding %s…\n", args[0])
			result, err := c.RebuildChallenge(args[0])
			if err != nil {
				return err
			}
			fmt.Printf("✅ Rebuilt: image=%v  duration=%vms\n",
				result["image"], result["duration_ms"])
			return nil
		},
	}
}

func formatInts(ints []int) string {
	if len(ints) == 0 {
		return "—"
	}
	strs := make([]string, len(ints))
	for i, v := range ints {
		strs[i] = strconv.Itoa(v)
	}
	return strings.Join(strs, ",")
}
