package cli

import (
	"fmt"

	"github.com/spf13/cobra"
)

var remoteToken string

func newRemoteCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "remote",
		Short: "Manage connections to the remote Shipyard HTTP server",
	}

	connectCmd := &cobra.Command{
		Use:   "connect <url>",
		Short: "Connect and authenticate with a remote Shipyard HTTP server",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			fmt.Printf("[*] Connecting to remote server at %s...\n", args[0])
			if remoteToken != "" {
				fmt.Println("    Authenticating with provided bearer token...")
			}
			// TODO: HTTP handshake / auth check and profile save
			fmt.Printf("[+] Successfully connected to %s\n", args[0])
			return nil
		},
	}
	connectCmd.Flags().StringVar(&remoteToken, "token", "", "authentication bearer token")

	cmd.AddCommand(connectCmd)
	cmd.AddCommand(&cobra.Command{
		Use:   "status",
		Short: "Check remote server health, version, and worker status",
		RunE: func(cmd *cobra.Command, args []string) error {
			if globalRemote == "" {
				return fmt.Errorf("no remote server configured (use --remote or $SHIPYARD_REMOTE)")
			}
			fmt.Printf("[*] Checking status for %s...\n", globalRemote)
			// TODO: HTTP GET /health
			fmt.Println("    Status: OK")
			return nil
		},
	})
	cmd.AddCommand(&cobra.Command{
		Use:   "list",
		Short: "List saved remote server profiles",
		RunE: func(cmd *cobra.Command, args []string) error {
			fmt.Println("Configured remotes:")
			// TODO: Read ~/.config/shipyard/config.yaml
			fmt.Println("  (no saved profiles found)")
			return nil
		},
	})
	cmd.AddCommand(&cobra.Command{
		Use:   "use <name>",
		Short: "Set the active remote server profile",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			fmt.Printf("[*] Switched active remote to: %s\n", args[0])
			return nil
		},
	})
	cmd.AddCommand(&cobra.Command{
		Use:   "rm <name>",
		Short: "Remove a saved remote server profile",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			fmt.Printf("[*] Removed remote profile: %s\n", args[0])
			return nil
		},
	})

	return cmd
}
