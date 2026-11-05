package cli

import (
	"os"
	"path/filepath"
	"testing"
)

func TestStartCPUProfileDisabled(t *testing.T) {
	stop, err := startCPUProfile("")
	if err != nil {
		t.Fatal(err)
	}
	stop()
}

func TestStartCPUProfileWritesFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "cpu.prof")
	stop, err := startCPUProfile(path)
	if err != nil {
		t.Fatal(err)
	}
	stop()

	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Size() == 0 {
		t.Error("CPU profile is empty")
	}
}

func TestStartCPUProfileCreateError(t *testing.T) {
	if _, err := startCPUProfile(filepath.Join(t.TempDir(), "missing", "cpu.prof")); err == nil {
		t.Error("expected an error for an unwritable path")
	}
}

// TestStartProfilingWiring covers the flag-parsing hook: the profile must be
// started from the parsed flag value, not before it is populated.
func TestStartProfilingWiring(t *testing.T) {
	path := filepath.Join(t.TempDir(), "cpu.prof")
	prev := cpuProfilePath
	cpuProfilePath = path
	t.Cleanup(func() {
		cpuProfilePath = prev
		stopCPUProfile = nil
	})

	if err := startProfiling(nil, nil); err != nil {
		t.Fatal(err)
	}
	if stopCPUProfile == nil {
		t.Fatal("profiling was not started")
	}
	stopCPUProfile()

	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Size() == 0 {
		t.Error("CPU profile is empty")
	}
}
