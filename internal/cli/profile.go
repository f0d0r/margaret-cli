package cli

import (
	"fmt"
	"os"
	"runtime/pprof"

	"github.com/spf13/cobra"
)

// cpuProfilePath is the value of --cpuprofile. When set, the whole command
// writes a pprof CPU profile to that file.
var cpuProfilePath string

// stopCPUProfile flushes the profile started by startProfiling from the root
// command's pre-run hook. It is nil when profiling is disabled.
var stopCPUProfile func()

// startProfiling starts CPU profiling when --cpuprofile is set. It runs as the
// root command's PersistentPreRunE, which is after flag parsing but before the
// subcommand's RunE.
func startProfiling(*cobra.Command, []string) error {
	stop, err := startCPUProfile(cpuProfilePath)
	if err != nil {
		return err
	}
	stopCPUProfile = stop
	return nil
}

// startCPUProfile starts CPU profiling into path and returns a function that
// flushes and closes the profile. An empty path disables profiling and returns
// a no-op stop function.
func startCPUProfile(path string) (stop func(), err error) {
	if path == "" {
		return func() {}, nil
	}
	f, err := os.Create(path)
	if err != nil {
		return nil, fmt.Errorf("create CPU profile: %w", err)
	}
	if err := pprof.StartCPUProfile(f); err != nil {
		_ = f.Close()
		return nil, fmt.Errorf("start CPU profile: %w", err)
	}
	return func() {
		pprof.StopCPUProfile()
		if err := f.Close(); err != nil {
			fmt.Fprintf(os.Stderr, "warning: close CPU profile %q: %v\n", path, err)
		}
	}, nil
}
