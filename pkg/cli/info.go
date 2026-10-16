package cli

import (
	"fmt"

	"github.com/spf13/cobra"
	"shipyard/pkg/shipfile"
)

func newInfoCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "info",
		Short: "Print metadata from the configured shipfile",
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
				return err
			}

			fmt.Printf("Shipfile: %s\n", sf.Path)
			printAttr("Name", sf.Name)
			printAttr("Url", sf.Url)
			if len(sf.Urls) > 0 {
				printAttr("Urls", fmt.Sprintf("%v", sf.Urls))
			}
			printAttr("VersionTags", sf.VersionTags)
			printAttr("PackageName", sf.PackageName)
			if sf.HasSetup {
				printAttr("Setup", "(defined in Shipfile.setup)")
			}
			if sf.HasBuild {
				printAttr("Build", "(defined in Shipfile.build)")
			}
			printAttr("Artifacts", sf.Artifacts)
			printAttr("ArtifactDir", sf.ArtifactDir)
			if len(sf.Variables) > 0 {
				fmt.Println("  Variables:")
				for _, v := range sf.Variables {
					if v.Description != "" {
						fmt.Printf("    %-16s = %q  (%s)\n", v.Name, v.Value, v.Description)
					} else {
						fmt.Printf("    %-16s = %q\n", v.Name, v.Value)
					}
				}
			}
			return nil
		},
	}
}

func printAttr(key, val string) {
	if val != "" {
		fmt.Printf("  %-14s %s\n", key+":", val)
	}
}
