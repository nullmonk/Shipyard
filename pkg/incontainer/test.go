package incontainer

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"
	"shipyard/pkg/shipfile"
)

func newTestCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "test",
		Short: "Run @test functions defined in the shipfile inside the container",
		Args:  cobra.NoArgs,
		RunE:  runContainerTest,
	}
}

func runContainerTest(_ *cobra.Command, _ []string) error {
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
		return fmt.Errorf("load shipfile: %w", err)
	}
	if !sf.IsTest() {
		return nil
	}

	version := os.Getenv("SHIPYARD_VERSION")
	sourceDir := os.Getenv("SHIPYARD_SOURCE")
	if sourceDir != "" {
		if err := os.Chdir(sourceDir); err != nil {
			return fmt.Errorf("chdir to SHIPYARD_SOURCE %s: %w", sourceDir, err)
		}
	}

	fmt.Println("[*] Running tests...")
	results, err := sf.RunTests(shipfile.RunContext{
		SourceDir: sourceDir,
		Version:   version,
	})
	if err != nil {
		return fmt.Errorf("test runner failed: %w", err)
	}

	failed := 0
	for _, r := range results {
		label := r.Name
		if label == "" {
			label = "(unnamed)"
		}
		if r.Passed {
			fmt.Printf("[+] PASS  %s\n", label)
		} else {
			fmt.Printf("[-] FAIL  %s: %s\n", label, r.Error)
			failed++
		}
	}
	if failed > 0 {
		return fmt.Errorf("%d test(s) failed", failed)
	}
	fmt.Printf("[+] All %d test(s) passed\n", len(results))
	return nil
}

