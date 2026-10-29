package cli

import (
	"fmt"
	"os"
	"sort"
	"strings"

	"github.com/spf13/cobra"
)

var versionsAll bool

// versionEntry is one upstream tag with its shipfile-mapped version.
type versionEntry struct {
	Tag     string
	Version string
	Ignored bool
}

func newVersionsCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "versions [directory]",
		Short: "List upstream versions for the shipfile",
		Long: `List upstream tags filtered by the shipfile VersionTags glob, mapped to
versions via tag_to_version (is_version_ignored versions are marked).

With no directory, tags are read from the remote (git ls-remote). With a
directory pointing at a previously pulled source tree, local tags are listed
instead (works offline).`,
		Args: cobra.MaximumNArgs(1),
		RunE: runVersions,
	}
	cmd.Flags().BoolVarP(&versionsAll, "all", "a", false, "include versions ignored by is_version_ignored")
	return cmd
}

func runVersions(cmd *cobra.Command, args []string) error {
	sfPath, err := getShipfilePath()
	if err != nil {
		return err
	}
	interp, err := getInterpreter()
	if err != nil {
		return fmt.Errorf("init interpreter: %w", err)
	}
	defer interp.Close()

	sf, err := loadAndValidateShipfile(interp, sfPath)
	if err != nil {
		return err
	}

	if sf.Url == "" {
		fmt.Printf("[*] %s has no upstream git URL (distro-package shipfile).\n", sf.Name)
		fmt.Println("    Versions come from the tracked mirrors; see `drydock versions`.")
		return nil
	}

	var tags []string
	if len(args) > 0 {
		tags, err = localTags(args[0])
		if err != nil {
			return err
		}
		fmt.Printf("[*] Local tags for %s in %s\n", sf.Name, args[0])
	} else {
		out, err := runGit("", "ls-remote", "--tags", sf.Url)
		if err != nil {
			return fmt.Errorf("list remote tags for %s: %w", sf.Url, err)
		}
		tags = parseLsRemoteTags(out)
		fmt.Printf("[*] Upstream versions for %s (%s)\n", sf.Name, sf.Url)
	}

	var entries []versionEntry
	for _, tag := range tags {
		if !matchVersionTags(sf.VersionTags, tag) {
			continue
		}
		ver, err := sf.TagToVersion(tag)
		if err != nil {
			fmt.Fprintf(os.Stderr, "[!] tag_to_version(%q) failed: %v\n", tag, err)
			continue
		}
		ignored := false
		if ign, err := sf.IsVersionIgnored(ver); err == nil {
			ignored = ign
		}
		entries = append(entries, versionEntry{Tag: tag, Version: ver, Ignored: ignored})
	}
	sort.Slice(entries, func(i, j int) bool {
		return compareVersions(entries[i].Version, entries[j].Version) < 0
	})

	shown := 0
	skipped := 0
	for _, e := range entries {
		if e.Ignored && !versionsAll {
			skipped++
			continue
		}
		marker := ""
		if e.Ignored {
			marker = "  (ignored)"
		}
		// Keep the tag visible: several tags can map oddly, and checkout
		// accepts either form.
		fmt.Printf("  %s  [%s]%s\n", e.Version, e.Tag, marker)
		shown++
	}
	fmt.Printf("[+] %d version(s) (tags matching %q", shown, sf.VersionTags)
	if skipped > 0 {
		fmt.Printf(", %d ignored — pass --all to show", skipped)
	}
	fmt.Println(")")
	return nil
}

// localTags returns tag names in a local clone.
func localTags(dir string) ([]string, error) {
	out, err := runGit(dir, "tag", "--list")
	if err != nil {
		return nil, fmt.Errorf("list local tags in %s: %w", dir, err)
	}
	var tags []string
	for _, t := range strings.Split(out, "\n") {
		if t = strings.TrimSpace(t); t != "" {
			tags = append(tags, t)
		}
	}
	return tags, nil
}
