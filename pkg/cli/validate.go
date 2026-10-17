package cli

import (
	"fmt"

	"github.com/spf13/cobra"
	"shipyard/pkg/shipfile"
)

func newValidateCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "validate",
		Short: "Validate that the shipfile satisfies builder or patcher rules",
		RunE: func(cmd *cobra.Command, args []string) error {
			sfPath, err := getShipfilePath()
			if err != nil {
				return err
			}
			interp, err := getInterpreter()
			if err != nil {
				return fmt.Errorf("init interpreter: %w", err)
			}
			defer interp.Close()

			sf, err := shipfile.Load(sfPath, interp)
			if err != nil {
				return fmt.Errorf("load shipfile %s: %w", sfPath, err)
			}
			if err := sf.Validate(); err != nil {
				return fmt.Errorf("shipfile %s is invalid: %w", sfPath, err)
			}
			fmt.Printf("Shipfile %s is valid.\n", sfPath)
			return nil
		},
	}
}
