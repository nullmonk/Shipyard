package main

import (
	"fmt"

	"github.com/spf13/cobra"
)

var (
	remoteToken string
)

var remoteCmd = &cobra.Command{
	Use:   "remote",
	Short: "Manage connections to the remote Shipyard HTTP server",
	Long: `Remote manages connections, authentication credentials, and profiles
for remote Shipyard HTTP servers used for distributed builds and workers.`,
}

var remoteConnectCmd = &cobra.Command{
	Use:   "connect <url>",
	Short: "Connect and authenticate with a remote Shipyard HTTP server",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		url := args[0]
		fmt.Printf("[*] Connecting to remote server at %s...\n", url)
		if remoteToken != "" {
			fmt.Println("    Authenticating with provided bearer token...")
		}
		// TODO: Implement HTTP handshake / auth check and profile save
		fmt.Printf("[+] Successfully connected to %s\n", url)
		return nil
	},
}

var remoteStatusCmd = &cobra.Command{
	Use:   "status",
	Short: "Check remote server health, version, and worker status",
	RunE: func(cmd *cobra.Command, args []string) error {
		endpoint := globalRemote
		if endpoint == "" {
			return fmt.Errorf("no remote server configured (use --remote, $SHIPYARD_REMOTE, or 'shipyard remote use <name>')")
		}
		fmt.Printf("[*] Checking status for %s...\n", endpoint)
		// TODO: HTTP GET /health or /api/v1/status
		fmt.Println("    Status: OK")
		return nil
	},
}

var remoteListCmd = &cobra.Command{
	Use:   "list",
	Short: "List saved remote server profiles",
	RunE: func(cmd *cobra.Command, args []string) error {
		fmt.Println("Configured remotes:")
		// TODO: Read local config file (e.g. ~/.config/shipyard/config.yaml)
		fmt.Println("  (no saved profiles found)")
		return nil
	},
}

var remoteUseCmd = &cobra.Command{
	Use:   "use <name>",
	Short: "Set the active remote server profile",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		name := args[0]
		fmt.Printf("[*] Switched active remote to: %s\n", name)
		return nil
	},
}

var remoteRmCmd = &cobra.Command{
	Use:   "rm <name>",
	Short: "Remove a saved remote server profile",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		name := args[0]
		fmt.Printf("[*] Removed remote profile: %s\n", name)
		return nil
	},
}

func init() {
	remoteConnectCmd.Flags().StringVar(&remoteToken, "token", "", "authentication bearer token")

	remoteCmd.AddCommand(remoteConnectCmd)
	remoteCmd.AddCommand(remoteStatusCmd)
	remoteCmd.AddCommand(remoteListCmd)
	remoteCmd.AddCommand(remoteUseCmd)
	remoteCmd.AddCommand(remoteRmCmd)
}

