// shipyard executes and builds shipfile projects using Dagger and gpython.
package main

import (
	"os"

	"shipyard/pkg/cli"
	"shipyard/pkg/incontainer"
)

func main() {
	var root = cli.Root()
	if os.Getenv("SHIPYARD_IN_CONTAINER") == "1" {
		root = incontainer.Root()
	}
	if err := root.Execute(); err != nil {
		os.Exit(1)
	}
}
