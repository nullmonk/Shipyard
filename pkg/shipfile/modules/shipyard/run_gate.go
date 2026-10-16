package shipmod

import (
	"fmt"
	"sync"

	"github.com/go-python/gpython/py"
)

var (
	runGateMu      sync.RWMutex
	enableRunHook  func(py.Context) error
	disableRunHook func(py.Context) error
)

// SetRunGateHooks configures the callbacks for EnableRun and DisableRun.
func SetRunGateHooks(enable, disable func(py.Context) error) {
	runGateMu.Lock()
	defer runGateMu.Unlock()
	enableRunHook = enable
	disableRunHook = disable
}

// EnableRun unlocks Run() for the given interpreter context.
func EnableRun(ctx py.Context) error {
	runGateMu.RLock()
	fn := enableRunHook
	runGateMu.RUnlock()
	if fn != nil {
		return fn(ctx)
	}
	return fmt.Errorf("run gate hooks not configured")
}

// DisableRun locks Run() again for the given interpreter context.
func DisableRun(ctx py.Context) error {
	runGateMu.RLock()
	fn := disableRunHook
	runGateMu.RUnlock()
	if fn != nil {
		return fn(ctx)
	}
	return fmt.Errorf("run gate hooks not configured")
}

