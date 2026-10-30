package shipmod

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// TestBuildShadowDir is the fixed absolute path inside a fresh fan-out test
// container where the host mounts the BUILD container's root filesystem.
//
// Test-phase Copy semantics (multi-stage copy): Copy(src, dst) called while
// no ContainerBuilder is active — i.e. inside @test functions, pre_test, or
// post_test — resolves src against the BUILD container's filesystem instead
// of the host/Caller context used by build-phase Copy:
//
//	Copy("path") / Copy("path", dst)  -> <shadow>/path -> dst
//	Copy("build:path", dst)            -> <shadow>/path -> dst
//
// Any other explicit stage ("other:path") is an error: named build stages do
// not exist in the test phase. Container-local copies are NOT served by this
// fallback; use Run("cp ...") for those. Glob patterns (*?[]) in src are
// expanded against the shadow tree; zero matches is an error (so
// SaveIfExists-style helpers keep working when composed with Copy).
//
// Build-phase Copy behavior is unchanged: while a ContainerBuilder is active,
// copyFn routes to it and this fallback is never consulted.
const TestBuildShadowDir = "/work/shipyard/build"

// testCopyAvailableAt reports whether dir looks like a mounted build shadow.
func testCopyAvailableAt(dir string) bool {
	fi, err := os.Stat(dir)
	return err == nil && fi.IsDir()
}

// TestCopyAvailable reports whether the calling process runs in a test-phase
// container prepared by the host fan-out path (build shadow present).
func TestCopyAvailable() bool {
	return testCopyAvailableAt(TestBuildShadowDir)
}

// ResolveTestCopySrc maps a test-phase Copy src argument to an absolute
// filesystem path under shadowRoot plus whether it is a glob pattern.
// A bare "src" (no stage prefix) and an explicit "build:src" both resolve
// against the build container tree; any other stage is rejected.
func ResolveTestCopySrc(src, shadowRoot string) (absPath string, isGlob bool, err error) {
	ctr, p := parseCopySrc(src)
	switch ctr {
	case "", "build":
		// Plain paths resolve against the build container.
	default:
		return "", false, fmt.Errorf("unknown stage %q in test phase: only the build container (\"build\") is available for Copy", ctr)
	}
	if p == "" || p == "." || p == "./" {
		return shadowRoot, false, nil
	}
	isGlob = strings.ContainsAny(p, "*?[]")
	joined := filepath.Join(shadowRoot, filepath.FromSlash(p))
	return joined, isGlob, nil
}

// copyFromBuild implements the test-phase Copy fallback against an explicit
// shadow root (TestPhaseCopy wraps it with TestBuildShadowDir). dst honours
// the process working directory for relative paths; a dst naming an existing
// directory (or ending in a separator) receives each source basename inside
// it. Multiple glob matches require a directory dst.
func copyFromBuild(src, dst, shadowRoot string) error {
	srcPath, isGlob, err := ResolveTestCopySrc(src, shadowRoot)
	if err != nil {
		return err
	}

	dstPath := dst
	if dstPath == "" || dstPath == "." {
		dstPath = "."
	}
	if !filepath.IsAbs(dstPath) {
		cwd, err := os.Getwd()
		if err != nil {
			return fmt.Errorf("resolve Copy dst %q: %w", dst, err)
		}
		dstPath = filepath.Join(cwd, dstPath)
	}

	var items []string
	if isGlob {
		matches, err := filepath.Glob(srcPath)
		if err != nil {
			return fmt.Errorf("Copy(%q): bad pattern: %w", src, err)
		}
		if len(matches) == 0 {
			return fmt.Errorf("Copy(%q): no files matched", src)
		}
		items = matches
	} else {
		if _, err := os.Stat(srcPath); err != nil {
			return fmt.Errorf("Copy(%q): stat build file %q: %w", src, srcPath, err)
		}
		items = []string{srcPath}
	}

	dstIsDir := strings.HasSuffix(dst, "/") || strings.HasSuffix(dst, string(os.PathSeparator))
	if fi, err := os.Stat(dstPath); err == nil && fi.IsDir() {
		dstIsDir = true
	}
	if len(items) > 1 && !dstIsDir {
		if err := os.MkdirAll(dstPath, 0o755); err != nil {
			return fmt.Errorf("Copy(%q): create dst dir %q: %w", src, dstPath, err)
		}
		dstIsDir = true
	}

	for _, item := range items {
		target := dstPath
		if dstIsDir {
			target = filepath.Join(dstPath, filepath.Base(item))
		}
		if err := copyPathRecursive(item, target); err != nil {
			return fmt.Errorf("Copy(%q): %w", src, err)
		}
	}
	return nil
}

// TestPhaseCopy copies src (resolved against the build shadow) to dst in the
// local test-container filesystem. It backs Copy() called from @test
// functions when no ContainerBuilder is active.
func TestPhaseCopy(src, dst string) error {
	return copyFromBuild(src, dst, TestBuildShadowDir)
}

// copyPathRecursive copies a single file or directory tree from src to dst,
// creating parent directories and preserving file mode bits.
func copyPathRecursive(src, dst string) error {
	fi, err := os.Stat(src)
	if err != nil {
		return err
	}
	if !fi.IsDir() {
		if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
			return err
		}
		data, err := os.ReadFile(src)
		if err != nil {
			return err
		}
		return os.WriteFile(dst, data, fi.Mode().Perm())
	}
	if err := os.MkdirAll(dst, 0o755); err != nil {
		return err
	}
	entries, err := os.ReadDir(src)
	if err != nil {
		return err
	}
	for _, e := range entries {
		if err := copyPathRecursive(filepath.Join(src, e.Name()), filepath.Join(dst, e.Name())); err != nil {
			return err
		}
	}
	return nil
}
