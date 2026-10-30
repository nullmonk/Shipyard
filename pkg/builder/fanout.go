package builder

import (
	"context"
	"errors"
	"fmt"
	"io"
	"path"
	"path/filepath"
	"strings"
	"time"

	"dagger.io/dagger"
	"shipyard/pkg/builder/drivers"
	"shipyard/pkg/shipfile"
	shipmod "shipyard/pkg/shipfile/modules/shipyard"
)

// This file holds the shared helpers for phased builds with multi-system
// test fan-out (see phases.go for the marker contract).
//
// Fan-out test containers are FRESH: FROM the --test-system image ref, FULL
// distro driver Setup (so repos exist and InstallPackage file+name forms
// work), build outputs staged at identical paths plus the whole build
// container rootfs mounted at shipmod.TestBuildShadowDir (Copy-from-build,
// multi-stage copy semantics), then the @test functions run in-container via
// `shipyard test` — the same runner as the legacy in-place path.

// syncContainerStep syncs ctr, reporting label progress on out and command
// output on cmdOut/cmdErr. It mirrors the legacy per-flow sync closures so
// fan-out steps report identically.
func syncContainerStep(ctx context.Context, ctr *dagger.Container, label string, out, cmdOut, cmdErr io.Writer) (*dagger.Container, error) {
	fmt.Fprintf(out, "%s... ", label)
	t0 := time.Now()
	synced, err := ctr.Sync(ctx)
	elapsed := time.Since(t0)
	if err != nil {
		fmt.Fprintln(out, "FAILED")
		var execErr *dagger.ExecError
		if errors.As(err, &execErr) {
			if execErr.Stdout != "" {
				outStr := execErr.Stdout
				if !strings.HasSuffix(outStr, "\n") {
					outStr += "\n"
				}
				fmt.Fprint(cmdOut, outStr)
				if cmdOut == io.Discard {
					fmt.Fprint(out, outStr)
				}
			}
			if execErr.Stderr != "" {
				errStr := execErr.Stderr
				if !strings.HasSuffix(errStr, "\n") {
					errStr += "\n"
				}
				fmt.Fprint(cmdErr, errStr)
			}
		}
		return nil, fmt.Errorf("%s: %w", label, err)
	}
	fmt.Fprintf(out, "done (%.1fs)\n", elapsed.Seconds())
	return synced, nil
}

// withBuildShadow mounts the build container's root filesystem at the fixed
// test-copy shadow path so in-container Copy() calls in @test functions
// resolve src against the build tree.
func withBuildShadow(testCtr, buildCtr *dagger.Container) *dagger.Container {
	return testCtr.WithDirectory(shipmod.TestBuildShadowDir, buildCtr.Rootfs())
}

// stageSavesForTest copies every recorded generic-build Save output from its
// snapshot container into the test container at the identical absolute path,
// so InstallPackage file-form targets and test globs keep working.
func stageSavesForTest(testCtr *dagger.Container, saves []SaveOp) *dagger.Container {
	for _, s := range saves {
		dst := s.SrcPath
		if !path.IsAbs(dst) {
			dst = "/" + strings.TrimPrefix(dst, "/")
		}
		if s.IsDir {
			testCtr = testCtr.WithDirectory(dst, s.Container.Directory(s.SrcPath))
		} else {
			testCtr = testCtr.WithFile(dst, s.Container.File(s.SrcPath))
		}
	}
	return testCtr
}

// execShipTests runs `shipyard test` in ctr and streams the test report to
// cmdOut. It matches the legacy in-container test handling: exec stdout (and
// stderr on failure) is surfaced, and any failure is returned so the caller
// fails the test phase (and the overall run).
func execShipTests(ctx context.Context, ctr *dagger.Container, cmdOut, cmdErr io.Writer) (*dagger.Container, error) {
	synced, err := ctr.WithExec([]string{"shipyard", "test"}).Sync(ctx)
	if err != nil {
		var execErr *dagger.ExecError
		if errors.As(err, &execErr) {
			if execErr.Stdout != "" {
				outStr := execErr.Stdout
				if !strings.HasSuffix(outStr, "\n") {
					outStr += "\n"
				}
				fmt.Fprint(cmdOut, outStr)
			}
			if execErr.Stderr != "" {
				errStr := execErr.Stderr
				if !strings.HasSuffix(errStr, "\n") {
					errStr += "\n"
				}
				fmt.Fprint(cmdErr, errStr)
			}
		}
		return nil, err
	}
	if stdout, _ := synced.Stdout(ctx); stdout != "" {
		fmt.Fprint(cmdOut, stdout)
	}
	return synced, nil
}

// runGenericFanOutTests runs the @test functions once per --test-system image
// ref in a FRESH container: FROM the ref, best-effort distro driver Setup (so
// repos exist for InstallPackage name-form installs), the recorded Save
// outputs staged at identical paths, and the whole build container rootfs
// mounted at the test-copy shadow (Copy-from-build). Tests execute
// in-container via `shipyard test` — the same runner as the legacy in-place
// path. mOut receives one [drydock:phase:test:<ref>] marker per system ahead
// of that system's output. A failure in any single system is collected and
// returned after the remaining systems run, failing the overall run.
func runGenericFanOutTests(ctx context.Context, client *dagger.Client, sf *shipfile.Shipfile, buildCtr *dagger.Container, saves []SaveOp, ver string, systems []string, interactive, verbose bool, out, mOut, cmdOut, cmdErr io.Writer) error {
	shipfileHostFile := client.Host().File(filepath.Join(sf.Dir, "shipfile.py"))
	var firstErr error
	for _, sys := range systems {
		EmitMarker(mOut, TestPhaseMarker(sys))
		testCtr := client.Container().From(sys)
		if drv, derr := drivers.GetDriver(sys); derr == nil {
			testCtr = drv.Setup(testCtr)
		} else if verbose {
			fmt.Fprintf(out, "    (no distro driver for %s; skipping distro setup)\n", sys)
		}
		testCtr = withBuildShadow(testCtr, buildCtr)
		testCtr = stageSavesForTest(testCtr, saves)
		synced, err := syncContainerStep(ctx, testCtr, "[*] Preparing test system "+sys, out, cmdOut, cmdErr)
		if err != nil {
			if firstErr == nil {
				firstErr = fmt.Errorf("prepare test system %s: %w", sys, err)
			}
			continue
		}
		testCtr = synced.
			WithFile("/work/shipyard/shipfile.py", shipfileHostFile).
			WithFile("shipfile.py", shipfileHostFile).
			WithEnvVariable("SHIPYARD_IN_CONTAINER", "1").
			WithEnvVariable("SHIPFILE", "/work/shipyard/shipfile.py")
		if sf.Name == "shipyard" {
			// Prefer the container's own freshly-built binary, guaranteed compatible with container OS/libc
			testCtr = testCtr.WithExec([]string{"/bin/sh", "-c", "if [ -f bin/shipyard ]; then cp bin/shipyard /usr/local/bin/shipyard; fi"})
		} else {
			self, serr := findContainerBinary(sf.Dir)
			if serr != nil {
				if firstErr == nil {
					firstErr = fmt.Errorf("resolve shipyard binary for test system %s: %w", sys, serr)
				}
				continue
			}
			testCtr = testCtr.WithFile("/usr/local/bin/shipyard", client.Host().File(self), dagger.ContainerWithFileOpts{Permissions: 0o755})
		}
		if ver != "" {
			testCtr = testCtr.WithEnvVariable("SHIPYARD_VERSION", ver)
		}
		fmt.Fprintln(out, "[*] Running tests in container...")
		if _, err := execShipTests(ctx, testCtr, cmdOut, cmdErr); err != nil {
			if interactive {
				dropIntoShell(ctx, client, out, testCtr, sf.Dir)
			}
			if firstErr == nil {
				firstErr = fmt.Errorf("tests failed on %s: %w", sys, err)
			}
			continue
		}
	}
	return firstErr
}

// runPackageFanOutTests runs the @test functions once per --test-system image
// ref in a FRESH container: FROM the ref, FULL distro driver Setup for the
// test image (so repos exist and InstallPackage file+name forms work), the
// build artifact directory staged at the identical path, and the whole build
// container rootfs mounted at the test-copy shadow (Copy-from-build). Tests
// execute in-container via `shipyard test` — the same runner as the legacy
// in-place path. mOut receives one [drydock:phase:test:<ref>] marker per
// system ahead of that system's output. A failure in any single system is
// collected and returned after the remaining systems run.
func runPackageFanOutTests(ctx context.Context, client *dagger.Client, sf *shipfile.Shipfile, buildCtr *dagger.Container, buildDriver drivers.DistroDriver, ver, pkgName string, systems []string, interactive, verbose bool, out, mOut, cmdOut, cmdErr io.Writer) error {
	self, err := findContainerBinary(sf.Dir)
	if err != nil {
		return fmt.Errorf("resolve shipyard binary for test containers: %w", err)
	}
	selfFile := client.Host().File(self)
	shipfileDirExcludes := append([]string{".git/", "__pycache__/", "build-output/", "sources/"}, readIgnorePatterns(sf.Dir)...)
	shipfileDir := client.Host().Directory(sf.Dir, dagger.HostDirectoryOpts{
		Exclude: shipfileDirExcludes,
	})
	var firstErr error
	for _, sys := range systems {
		EmitMarker(mOut, TestPhaseMarker(sys))
		testDrv, derr := drivers.GetDriver(sys)
		if derr != nil {
			if firstErr == nil {
				firstErr = fmt.Errorf("test system %s: %w", sys, derr)
			}
			continue
		}
		testCtr := client.Container().From(sys)
		testCtr = testDrv.Setup(testCtr)
		artDir := buildDriver.GetArtifactDir()
		testCtr = testCtr.WithDirectory(artDir, buildCtr.Directory(artDir))
		testCtr = withBuildShadow(testCtr, buildCtr)
		synced, serr := syncContainerStep(ctx, testCtr, "[*] Preparing test system "+sys, out, cmdOut, cmdErr)
		if serr != nil {
			if firstErr == nil {
				firstErr = fmt.Errorf("prepare test system %s: %w", sys, serr)
			}
			continue
		}
		testCtr = synced
		// Parity with the legacy in-place path: the freshly built packages
		// are installed before @test functions run, so tests exercise the
		// new packages. Only valid when both images share a distro family;
		// otherwise the tests self-provision via InstallPackage/Copy.
		if testDrv.DistroName() == buildDriver.DistroName() {
			if installed, ierr := syncContainerStep(ctx, testCtr.WithExec([]string{"/bin/sh", "-c", testDrv.InstallArtifactsScript()}), "[*] Installing built packages", out, cmdOut, cmdErr); ierr != nil {
				if firstErr == nil {
					firstErr = fmt.Errorf("install built packages for tests on %s: %w", sys, ierr)
				}
				continue
			} else {
				testCtr = installed
			}
		} else {
			fmt.Fprintf(out, "    (skipping built-package install: test system %s is %s, build is %s; tests provision via InstallPackage/Copy)\n",
				sys, testDrv.DistroName(), buildDriver.DistroName())
		}
		distroVersion, _ := testCtr.
			WithExec([]string{"/bin/sh", "-c", `grep '^VERSION_ID=' /etc/os-release | cut -d= -f2 | tr -d '"'`}).
			Stdout(ctx)
		testCtr = testCtr.
			WithFile("/usr/local/bin/shipyard", selfFile, dagger.ContainerWithFileOpts{Permissions: 0o755}).
			WithDirectory("/work/shipyard", shipfileDir).
			WithEnvVariable("SHIPYARD_IN_CONTAINER", "1").
			WithEnvVariable("SHIPFILE", "/work/shipyard/shipfile.py").
			WithEnvVariable("SHIPYARD_PACKAGE", pkgName).
			WithEnvVariable("SHIPYARD_DISTRO", testDrv.DistroName()).
			WithEnvVariable("SHIPYARD_DISTRO_VERSION", strings.TrimSpace(distroVersion)).
			WithEnvVariable("SHIPYARD_TOPDIR", testDrv.TopDir())
		if ver != "" {
			testCtr = testCtr.WithEnvVariable("SHIPYARD_VERSION", ver)
		}
		fmt.Fprintln(out, "[*] Running tests in container...")
		if _, err := execShipTests(ctx, testCtr, cmdOut, cmdErr); err != nil {
			if interactive {
				dropIntoShell(ctx, client, out, testCtr, sf.Dir)
			}
			if firstErr == nil {
				firstErr = fmt.Errorf("tests failed on %s: %w", sys, err)
			}
			continue
		}
	}
	return firstErr
}
