package cli

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"
	"gopkg.in/yaml.v3"
	"shipyard/pkg/shipfile"
)

func newInfoCmd() *cobra.Command {
	var jsonOut bool
	cmd := &cobra.Command{
		Use:   "info [shipfile]",
		Short: "Print metadata from the configured shipfile as YAML (or JSON with -j)",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			target := ""
			if len(args) > 0 {
				target = args[0]
			}
			sfPath, err := resolveShipfilePath(target)
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

			relPath := sfPath
			if cwd, err := os.Getwd(); err == nil {
				if rel, err := filepath.Rel(cwd, sfPath); err == nil {
					relPath = rel
				}
			}

			var types []string
			if sf.ImplementsPatcher {
				types = append(types, "patcher")
			}
			if sf.ImplementsBuilder {
				types = append(types, "builder")
			}
			if sf.ImplementsTester {
				types = append(types, "tester")
			}

			type varOut struct {
				Name        string `yaml:"name"        json:"name"`
				Value       string `yaml:"value"       json:"value"`
				Description string `yaml:"description,omitempty" json:"description,omitempty"`
			}
			type infoOut struct {
				Path        string   `yaml:"path"                   json:"path"`
				Name        string   `yaml:"name,omitempty"         json:"name,omitempty"`
				Types       []string `yaml:"type,omitempty"         json:"type,omitempty"`
				Url         string   `yaml:"url,omitempty"          json:"url,omitempty"`
				Urls        []string `yaml:"urls,omitempty"         json:"urls,omitempty"`
				VersionTags string   `yaml:"version_tags,omitempty" json:"version_tags,omitempty"`
				PackageName string   `yaml:"package_name,omitempty" json:"package_name,omitempty"`
				Artifacts   string   `yaml:"artifacts,omitempty"    json:"artifacts,omitempty"`
				ArtifactDir string   `yaml:"artifact_dir,omitempty" json:"artifact_dir,omitempty"`
				Variables   []varOut `yaml:"variables,omitempty"    json:"variables,omitempty"`
			}

			out := infoOut{
				Path:        relPath,
				Name:        sf.Name,
				Types:       types,
				Url:         sf.Url,
				Urls:        sf.Urls,
				VersionTags: sf.VersionTags,
				PackageName: sf.PackageName,
				Artifacts:   sf.Artifacts,
				ArtifactDir: sf.ArtifactDir,
			}
			for _, v := range sf.Variables {
				out.Variables = append(out.Variables, varOut{
					Name:        v.Name,
					Value:       v.Value,
					Description: v.Description,
				})
			}

			if jsonOut {
				enc := json.NewEncoder(os.Stdout)
				enc.SetIndent("", "  ")
				return enc.Encode(out)
			}
			enc := yaml.NewEncoder(os.Stdout)
			enc.SetIndent(2)
			return enc.Encode(out)
		},
	}
	cmd.Flags().BoolVarP(&jsonOut, "json", "j", false, "output as JSON instead of YAML")
	return cmd
}
