package report

import (
	"encoding/json"
	"fmt"
	"os"

	"github.com/f0d0r/margaret-cli/internal/processor"
)

// failureReport is the JSON representation of one failed item.
type failureReport struct {
	Path  string `json:"path"`
	Error string `json:"error"`
}

// WriteFailures writes a JSON report of all failed items to path.
func WriteFailures(failures []processor.Failure, path string) error {
	report := make([]failureReport, 0, len(failures))
	for _, f := range failures {
		report = append(report, failureReport{Path: f.Path, Error: f.Err.Error()})
	}
	data, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		return err
	}
	if err := os.WriteFile(path, append(data, '\n'), 0o644); err != nil {
		return fmt.Errorf("write failure report: %w", err)
	}
	return nil
}
