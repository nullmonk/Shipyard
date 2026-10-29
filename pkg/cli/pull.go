package cli

import (
	"archive/tar"
	"archive/zip"
	"compress/bzip2"
	"compress/gzip"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/spf13/cobra"
	"shipyard/pkg/shipfile"
)

var (
	pullVersion string
	pullBranch  string
	pullForce   bool
)

func newPullCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "pull <directory> [flags]",
		Short: "Fetch upstream source into a local directory",
		Long: `Fetch the upstream source defined by the shipfile into a local
directory: git-clone a git Url (optionally at --version/--branch), or
download and unpack Urls archives into a freshly tagged git repo
("jumpstart" for sources with no git history).

Use --version with either a version string (mapped via version_to_tag)
or a raw tag name. Afterwards, ` + "`shipyard checkout`" + ` jumps between
versions and ` + "`shipyard patch`" + ` applies the CodePatches locally.`,
		Args: cobra.ExactArgs(1),
		RunE: runPull,
	}
	cmd.Flags().StringVarP(&pullVersion, "version", "v", "", "target version or tag to pull (defaults to default branch / HEAD)")
	cmd.Flags().StringVarP(&pullBranch, "branch", "b", "", "target branch to pull (defaults to Shipfile Branch)")
	cmd.Flags().BoolVar(&pullForce, "force", false, "overwrite destination directory if it already exists")
	cmd.Flags().BoolVar(&pullForce, "clean", false, "alias for --force")
	return cmd
}

func runPull(cmd *cobra.Command, args []string) error {
	destDir := args[0]

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

	absDest, err := filepath.Abs(destDir)
	if err != nil {
		return fmt.Errorf("resolve destination path: %w", err)
	}

	if fi, err := os.Stat(absDest); err == nil {
		if fi.IsDir() {
			entries, _ := os.ReadDir(absDest)
			if len(entries) > 0 && !pullForce {
				return fmt.Errorf("destination %s exists and is not empty (use --force to overwrite)", absDest)
			}
		} else if !pullForce {
			return fmt.Errorf("destination %s already exists and is not a directory (use --force to overwrite)", absDest)
		}
		if pullForce {
			if err := os.RemoveAll(absDest); err != nil {
				return fmt.Errorf("clear destination %s: %w", absDest, err)
			}
		}
	}

	branch := pullBranch
	if branch == "" {
		branch = sf.Branch
	}

	fmt.Printf("[*] Pulling source for %s to %s\n", sf.Name, absDest)
	switch {
	case sf.Url != "" && !isArchiveURL(sf.Url):
		fmt.Printf("    Source URL: %s\n", sf.Url)
		if err := gitClone(sf.Url, absDest, branch, pullVersion, sf); err != nil {
			return err
		}
	case sf.Url != "" && isArchiveURL(sf.Url):
		fmt.Printf("    Source archive: %s\n", sf.Url)
		if err := pullSingleArchive(sf.Url, absDest); err != nil {
			return err
		}
		if pullVersion != "" {
			fmt.Fprintf(os.Stderr, "[!] --version ignored for single-archive sources (no version history)\n")
		}
	case len(sf.Urls) > 0:
		fmt.Printf("    Source archives: %d URLs (jumpstart)\n", len(sf.Urls))
		if err := pullJumpstart(sf.Urls, absDest, pullVersion); err != nil {
			return err
		}
	default:
		return fmt.Errorf("shipfile defines neither Url nor Urls: nothing to pull (distro-package shipfiles fetch sources during `shipyard build`)")
	}

	fmt.Printf("[+] Pull completed: %s\n", absDest)
	fmt.Println("    Next: `shipyard checkout <version>` to jump versions, `shipyard patch` to apply CodePatches.")
	return nil
}

// gitClone clones url into dest, then checks out branch or version/tag.
func gitClone(url, dest, branch, version string, sf *shipfile.Shipfile) error {
	if out, err := runGit("", "clone", url, dest); err != nil {
		return fmt.Errorf("git clone %s failed: %v\n%s", url, err, out)
	}
	switch {
	case branch != "":
		if out, err := runGit(dest, "checkout", branch); err != nil {
			return fmt.Errorf("checkout branch %s failed: %v\n%s", branch, err, out)
		}
		fmt.Printf("    Branch: %s\n", branch)
	case version != "":
		tag := resolveTag(sf, version)
		if out, err := runGit(dest, "checkout", tag); err != nil {
			return fmt.Errorf("checkout %s (from %q) failed: %v\n%s", tag, version, err, out)
		}
		fmt.Printf("    Version: %s [%s]\n", version, tag)
	}
	return nil
}

// pullSingleArchive downloads one archive URL and extracts it into dest.
func pullSingleArchive(rawurl, dest string) error {
	tmp, err := os.CreateTemp("", "shipyard-pull-*.archive")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	tmp.Close()
	defer os.Remove(tmpPath)
	fmt.Printf("    Downloading %s ...\n", rawurl)
	if err := downloadFile(rawurl, tmpPath); err != nil {
		return err
	}
	if err := os.MkdirAll(dest, 0o755); err != nil {
		return err
	}
	if err := extractArchive(tmpPath, dest); err != nil {
		return err
	}
	return nil
}

// pullJumpstart downloads every archive URL, imports each into a fresh git
// repo at dest (oldest first), and tags each import with a version derived
// from the archive file name. With version set, that tag is checked out.
func pullJumpstart(urls []string, dest, version string) error {
	tmpBase, err := os.MkdirTemp("", "shipyard-jumpstart-*")
	if err != nil {
		return err
	}
	defer os.RemoveAll(tmpBase)

	var tags []string
	for i, u := range urls {
		name := u
		if j := strings.LastIndex(name, "/"); j >= 0 {
			name = name[j+1:]
		}
		tag := versionFromArchiveName(name)
		fmt.Printf("    [%d/%d] %s -> tag %s\n", i+1, len(urls), u, tag)
		arcPath := filepath.Join(tmpBase, fmt.Sprintf("src-%d.archive", i))
		if err := downloadFile(u, arcPath); err != nil {
			return fmt.Errorf("download %s: %w", u, err)
		}
		stage := filepath.Join(tmpBase, fmt.Sprintf("stage-%d", i))
		if err := os.MkdirAll(stage, 0o755); err != nil {
			return err
		}
		if err := extractArchive(arcPath, stage); err != nil {
			return fmt.Errorf("extract %s: %w", u, err)
		}
		if i == 0 {
			if out, err := runGit("", "init", dest); err != nil {
				return fmt.Errorf("git init %s failed: %v\n%s", dest, err, out)
			}
		}
		if err := copyTree(stage, dest, map[string]bool{".git": true}); err != nil {
			return fmt.Errorf("import %s: %w", u, err)
		}
		if out, err := runGit(dest, "add", "-A"); err != nil {
			return fmt.Errorf("git add: %v\n%s", err, out)
		}
		// Empty imports (identical consecutive archives) still get a tag.
		commit := exec.Command("git", "-c", "user.name=shipyard", "-c", "user.email=shipyard@localhost", "commit", "-qm", tag, "--allow-empty")
		commit.Dir = dest
		if out, err := commit.CombinedOutput(); err != nil {
			return fmt.Errorf("git commit %s: %v\n%s", tag, err, out)
		}
		if out, err := runGit(dest, "tag", "-f", tag); err != nil {
			return fmt.Errorf("git tag %s: %v\n%s", tag, err, out)
		}
		tags = append(tags, tag)
	}
	if version != "" {
		want := version
		found := ""
		for _, t := range tags {
			if t == version {
				found = t
				break
			}
		}
		if found == "" {
			// Try mapping through the shipfile? Jumpstart tags are raw
			// versions already, so fall back to the raw input.
			found = want
		}
		if out, err := runGit(dest, "checkout", found); err != nil {
			return fmt.Errorf("checkout %s failed: %v\n%s", found, err, out)
		}
		fmt.Printf("    Version: %s\n", found)
	} else {
		fmt.Printf("    Tags: %s\n", strings.Join(tags, ", "))
	}
	return nil
}

// downloadFile fetches url into dstPath.
func downloadFile(rawurl, dstPath string) error {
	client := &http.Client{Timeout: 10 * time.Minute}
	resp, err := client.Get(rawurl)
	if err != nil {
		return fmt.Errorf("GET %s: %w", rawurl, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return fmt.Errorf("GET %s: unexpected status %s", rawurl, resp.Status)
	}
	out, err := os.Create(dstPath)
	if err != nil {
		return err
	}
	_, copyErr := io.Copy(out, resp.Body)
	closeErr := out.Close()
	if copyErr != nil {
		return copyErr
	}
	return closeErr
}

// extractArchive unpacks a .tar(.gz/.bz2), .tgz/.tbz2, or .zip archive into
// dest, stripping a single top-level directory component. .xz archives are
// delegated to the system tar binary.
func extractArchive(archivePath, dest string) error {
	lower := strings.ToLower(archivePath)
	switch {
	case strings.HasSuffix(lower, ".zip"):
		return extractZip(archivePath, dest)
	case strings.HasSuffix(lower, ".tar.xz") || strings.HasSuffix(lower, ".txz"):
		cmd := exec.Command("tar", "-xf", archivePath, "-C", dest, "--strip-components=1")
		if out, err := cmd.CombinedOutput(); err != nil {
			return fmt.Errorf("system tar extract %s: %v\n%s", archivePath, err, out)
		}
		return nil
	default:
		f, err := os.Open(archivePath)
		if err != nil {
			return err
		}
		defer f.Close()
		var tarReader *tar.Reader
		switch {
		case strings.HasSuffix(lower, ".tar.gz") || strings.HasSuffix(lower, ".tgz"):
			gz, err := gzip.NewReader(f)
			if err != nil {
				return fmt.Errorf("gzip reader: %w", err)
			}
			defer gz.Close()
			tarReader = tar.NewReader(gz)
		case strings.HasSuffix(lower, ".tar.bz2") || strings.HasSuffix(lower, ".tbz2"):
			tarReader = tar.NewReader(bzip2.NewReader(f))
		case strings.HasSuffix(lower, ".tar"):
			tarReader = tar.NewReader(f)
		default:
			return fmt.Errorf("unsupported archive format: %s (want .tar.gz/.tgz/.tar.bz2/.tbz2/.zip/.tar)", archivePath)
		}
		return untar(tarReader, dest, true)
	}
}

// untar extracts tar entries into dest, optionally stripping the first path
// component (top-level source directory).
func untar(tr *tar.Reader, dest string, stripTop bool) error {
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return err
		}
		name := filepath.ToSlash(hdr.Name)
		if stripTop {
			if i := strings.Index(name, "/"); i >= 0 {
				name = name[i+1:]
			} else {
				continue // top-level file itself; skip
			}
		}
		if name == "" {
			continue
		}
		target := filepath.Join(dest, filepath.FromSlash(name))
		// Guard against path traversal in archives.
		if rel, err := filepath.Rel(dest, target); err != nil || strings.HasPrefix(rel, "..") {
			return fmt.Errorf("archive entry escapes destination: %q", hdr.Name)
		}
		switch hdr.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(target, os.FileMode(hdr.Mode|0o755)); err != nil {
				return err
			}
		case tar.TypeReg, tar.TypeRegA:
			if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
				return err
			}
			out, err := os.OpenFile(target, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, os.FileMode(hdr.Mode))
			if err != nil {
				return err
			}
			_, copyErr := io.Copy(out, tr)
			closeErr := out.Close()
			if copyErr != nil {
				return copyErr
			}
			if closeErr != nil {
				return closeErr
			}
		case tar.TypeSymlink:
			if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
				return err
			}
			_ = os.Remove(target)
			if err := os.Symlink(hdr.Linkname, target); err != nil {
				return err
			}
		default:
			// Skip exotic entries (hardlinks, devices, ...).
		}
	}
}

// extractZip unpacks a .zip archive into dest, stripping a single top-level
// directory component.
func extractZip(archivePath, dest string) error {
	r, err := zip.OpenReader(archivePath)
	if err != nil {
		return err
	}
	defer r.Close()
	for _, f := range r.File {
		name := filepath.ToSlash(f.Name)
		if i := strings.Index(name, "/"); i >= 0 {
			name = name[i+1:]
		} else {
			continue
		}
		if name == "" {
			continue
		}
		target := filepath.Join(dest, filepath.FromSlash(name))
		if rel, err := filepath.Rel(dest, target); err != nil || strings.HasPrefix(rel, "..") {
			return fmt.Errorf("archive entry escapes destination: %q", f.Name)
		}
		if f.FileInfo().IsDir() {
			if err := os.MkdirAll(target, 0o755); err != nil {
				return err
			}
			continue
		}
		rc, err := f.Open()
		if err != nil {
			return err
		}
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			rc.Close()
			return err
		}
		out, err := os.OpenFile(target, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, f.Mode())
		if err != nil {
			rc.Close()
			return err
		}
		_, copyErr := io.Copy(out, rc)
		closeErr := out.Close()
		rc.Close()
		if copyErr != nil {
			return copyErr
		}
		if closeErr != nil {
			return closeErr
		}
	}
	return nil
}
