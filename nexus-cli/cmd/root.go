// Package cmd defines the Cobra root command and all subcommands for nexus-cli.
package cmd

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"
	"github.com/nexus-oss/nexus/nexus-cli/client"
	"github.com/nexus-oss/nexus/nexus-cli/config"
	"github.com/nexus-oss/nexus/nexus-cli/tui"

	tea "github.com/charmbracelet/bubbletea"
)

// Execute is the CLI entrypoint — called from main.go.
func Execute() {
	root := newRootCmd()
	if err := root.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func newRootCmd() *cobra.Command {
	var engineURL string
	var cfg *config.CLIConfig

	root := &cobra.Command{
		Use:   "nexus",
		Short: "Nexus OSS — control plane operator CLI",
		Long: `nexus is the operator CLI for the Nexus OSS infrastructure framework.

  Manage challenges, sessions, and inspect the reconciliation controller.
  Start the live TUI dashboard with: nexus tui

  Engine URL can be set via --engine flag or NEXUS_ENGINE_URL env var.`,
		PersistentPreRunE: func(cmd *cobra.Command, args []string) error {
			loaded, err := config.Load()
			if err != nil {
				return fmt.Errorf("load config: %w", err)
			}
			cfg = loaded
			// Flag overrides config.
			if engineURL != "" {
				cfg.EngineURL = engineURL
			}
			if envURL := os.Getenv("NEXUS_ENGINE_URL"); envURL != "" && engineURL == "" {
				cfg.EngineURL = envURL
			}
			return nil
		},
	}

	root.PersistentFlags().StringVar(&engineURL, "engine", "", "Nexus engine URL (default: http://localhost:8081)")

	// All subcommands receive the client via a factory func so they get the
	// resolved cfg.EngineURL after PersistentPreRunE runs.
	makeClient := func() *client.Client {
		if cfg == nil {
			cfg = config.Default()
		}
		return client.New(cfg.EngineURL)
	}

	// ── Subcommands ──────────────────────────────────────────────────────────
	root.AddCommand(
		newTUICmd(makeClient),
		newStatusCmd(makeClient),
		newChallengeCmd(makeClient()),
		newSessionCmd(makeClient()),
		newAdminCmd(makeClient),
		newConfigCmd(),
	)

	return root
}

// newTUICmd launches the live Bubbletea dashboard.
func newTUICmd(makeClient func() *client.Client) *cobra.Command {
	return &cobra.Command{
		Use:   "tui",
		Short: "Launch the live TUI dashboard",
		RunE: func(cmd *cobra.Command, args []string) error {
			c := makeClient()
			m := tui.New(c)
			p := tea.NewProgram(m, tea.WithAltScreen(), tea.WithMouseCellMotion())
			_, err := p.Run()
			return err
		},
	}
}

// newStatusCmd shows a quick engine health check.
func newStatusCmd(makeClient func() *client.Client) *cobra.Command {
	return &cobra.Command{
		Use:   "status",
		Short: "Show engine health and cluster overview",
		RunE: func(cmd *cobra.Command, args []string) error {
			c := makeClient()

			h, err := c.Health()
			if err != nil {
				fmt.Fprintf(os.Stderr, "❌ Engine unreachable: %v\n", err)
				os.Exit(1)
			}
			fmt.Printf("✅ Engine: %s | mode=%s | time=%s\n", h.Status, h.Mode, h.Timestamp)

			if sys, err := c.SystemInfo(); err == nil {
				fmt.Printf("   Sessions: %d  Pods: %d  Registry: %s\n",
					sys.SessionsTotal, sys.PodsTotal, sys.Registry)
			}
			if ctrl, err := c.ControllerStats(); err == nil {
				fmt.Printf("   Controller: %s | workers=%d | queued=%d | in-flight=%d\n",
					ctrl.Status, ctrl.Workers, ctrl.Queued, ctrl.InFlight)
			}
			return nil
		},
	}
}

// newAdminCmd groups admin operations.
func newAdminCmd(makeClient func() *client.Client) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "admin",
		Short: "Admin operations (cluster health, reconcile trigger)",
	}
	cmd.AddCommand(
		newAdminHealthCmd(makeClient),
		newAdminReconcileCmd(makeClient),
	)
	return cmd
}

func newAdminHealthCmd(makeClient func() *client.Client) *cobra.Command {
	return &cobra.Command{
		Use:   "health",
		Short: "Full cluster health (Redis + node agent + k3s)",
		RunE: func(cmd *cobra.Command, args []string) error {
			c := makeClient()
			resp, err := c.ClusterHealth()
			if err != nil {
				return err
			}
			for k, v := range resp {
				fmt.Printf("  %-16s %v\n", k+":", v)
			}
			return nil
		},
	}
}

func newAdminReconcileCmd(makeClient func() *client.Client) *cobra.Command {
	return &cobra.Command{
		Use:   "reconcile",
		Short: "Trigger an immediate reconcile for all active sessions",
		RunE: func(cmd *cobra.Command, args []string) error {
			c := makeClient()
			resp, err := c.TriggerReconcile()
			if err != nil {
				return err
			}
			fmt.Printf("✅ Reconcile triggered: %v session(s)\n", resp["sessions"])
			return nil
		},
	}
}

// newConfigCmd shows/edits CLI configuration.
func newConfigCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "config",
		Short: "Show or update nexus-cli configuration",
	}

	cmd.AddCommand(&cobra.Command{
		Use:   "show",
		Short: "Show current configuration",
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := config.Load()
			if err != nil {
				return err
			}
			fmt.Printf("Config file: %s\n", config.Path())
			fmt.Printf("  engine_url:    %s\n", cfg.EngineURL)
			fmt.Printf("  output_format: %s\n", cfg.OutputFormat)
			return nil
		},
	})

	cmd.AddCommand(&cobra.Command{
		Use:   "set-engine <url>",
		Short: "Set the nexus-engine URL",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := config.Load()
			if err != nil {
				return err
			}
			cfg.EngineURL = args[0]
			if err := cfg.Save(); err != nil {
				return err
			}
			fmt.Printf("✅ Engine URL set to %s\n", cfg.EngineURL)
			return nil
		},
	})

	return cmd
}
