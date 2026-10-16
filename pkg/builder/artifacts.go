package builder

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"dagger.io/dagger"
)

// FindFiles lists all files under the given directory in the container.
// Calling stdout forces the container pipeline execution, so any build errors
// will surface here.
func FindFiles(ctx context.Context, ctr *dagger.Container, directory string) ([]string, error) {
	output, err := ctr.
		WithWorkdir(directory).
		WithExec([]string{"find", ".", "-type", "f"}).
		Stdout(ctx)
	if err != nil {
		return nil, fmt.Errorf("list files in %s: %w", directory, err)
	}

	lines := strings.Split(output, "\n")
	var files []string
	for _, line := range lines {
		f := strings.TrimSpace(line)
		if f == "" {
			continue
		}
		if strings.HasPrefix(f, "./") {
			f = f[2:]
		}
		files = append(files, f)
	}
	return files, nil
}

// ExportArtifacts filters container files relative to srcDir using artifactPattern
// and exports all matching artifacts to outputDir on the host.
func ExportArtifacts(ctx context.Context, ctr *dagger.Container, files []string, srcDir, artifactPattern, outputDir string) (*dagger.Container, []string, error) {
	re, err := regexp.Compile(artifactPattern)
	if err != nil {
		return nil, nil, fmt.Errorf("invalid artifact regex %q: %w", artifactPattern, err)
	}

	var matches []string
	for _, f := range files {
		if re.MatchString(f) {
			matches = append(matches, f)
		}
	}

	if len(matches) == 0 {
		return nil, nil, fmt.Errorf("the build produced no artifacts matching %q in %s", artifactPattern, srcDir)
	}

	fmt.Printf("[*] Found %d artifacts matching '%s'\n", len(matches), artifactPattern)

	ctr = ctr.WithExec([]string{"mkdir", "-p", "/tmp/artifacts"})
	ctr = ctr.WithNewFile("/tmp/artifacts_list", strings.Join(matches, "\n"))

	copyCmd := fmt.Sprintf(
		"cd %s && while IFS= read -r f || [ -n \"$f\" ]; do cp \"$f\" /tmp/artifacts/; done < /tmp/artifacts_list",
		srcDir,
	)
	ctr = ctr.WithExec([]string{"/bin/sh", "-c", copyCmd})

	absOutput, err := filepath.Abs(outputDir)
	if err != nil {
		return nil, nil, fmt.Errorf("resolve output directory %s: %w", outputDir, err)
	}
	if err := os.MkdirAll(absOutput, 0o755); err != nil {
		return nil, nil, fmt.Errorf("create output directory %s: %w", absOutput, err)
	}

	if _, err := ctr.Directory("/tmp/artifacts").Export(ctx, absOutput); err != nil {
		return nil, nil, fmt.Errorf("export artifacts: %w", err)
	}

	fmt.Printf("[+] Build complete. Artifacts exported to %s:\n", absOutput)
	for _, m := range matches {
		fmt.Printf("  - %s\n", filepath.Base(m))
	}

	return ctr, matches, nil
}

