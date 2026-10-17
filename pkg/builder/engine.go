package builder

import (
	"context"
	"fmt"
	"io"
	"os"

	"dagger.io/dagger"
)

// Engine manages the connection to a local or remote Dagger engine.
type Engine struct {
	client *dagger.Client
}

// Connect initializes a Dagger client connection.
// If remote is specified (or SHIPYARD_DAGGER_REMOTE is set), the engine host
// is redirected to that runner endpoint (e.g. tcp://host:1234, docker-container://name).
// When verbose is true, Dagger engine logs are forwarded to stderr.
func Connect(ctx context.Context, remote string, verbose bool) (*Engine, error) {
	if remote == "" {
		remote = os.Getenv("SHIPYARD_DAGGER_REMOTE")
	}
	if remote != "" {
		if err := os.Setenv("_EXPERIMENTAL_DAGGER_RUNNER_HOST", remote); err != nil {
			return nil, fmt.Errorf("set dagger runner host: %w", err)
		}
		fmt.Fprintf(os.Stderr, "[*] Connecting to remote Dagger engine: %s\n", remote)
	}

	logOutput := io.Discard
	if verbose {
		logOutput = os.Stderr
	}

	client, err := dagger.Connect(ctx, dagger.WithLogOutput(logOutput))
	if err != nil {
		return nil, fmt.Errorf("dagger connect: %w", err)
	}

	return &Engine{client: client}, nil
}

// Client returns the underlying *dagger.Client.
func (e *Engine) Client() *dagger.Client {
	return e.client
}

// Close closes the Dagger engine session.
func (e *Engine) Close() error {
	if e.client != nil {
		return e.client.Close()
	}
	return nil
}

