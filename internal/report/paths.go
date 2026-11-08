package report

import (
	"fmt"
	"os"
	"path/filepath"
)

// DefaultDir is the directory used for generated reports when no explicit
// path is given.
const DefaultDir = "reports"

// DefaultPath returns the default path for a report inside DefaultDir.
func DefaultPath(name string) string {
	return filepath.Join(DefaultDir, name)
}

// writeFile writes data with a trailing newline, creating the parent
// directory when necessary.
func writeFile(path string, data []byte) error {
	if dir := filepath.Dir(path); dir != "." && dir != "" {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return fmt.Errorf("create report directory %q: %w", dir, err)
		}
	}
	return os.WriteFile(path, append(data, '\n'), 0o644)
}
