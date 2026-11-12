package report

import (
	"encoding/json"
	"fmt"
	"sort"

	"github.com/f0d0r/margaret-cli/internal/processor"
)

// skipReport is the JSON representation of one skipped item.
type skipReport struct {
	Path   string `json:"path"`
	Reason string `json:"reason"`
}

// WriteSkips writes a JSON report of all skipped items to path. Entries are
// sorted by path so repeated runs produce comparable reports.
func WriteSkips(skips []processor.Skip, path string) error {
	report := make([]skipReport, 0, len(skips))
	for _, s := range skips {
		report = append(report, skipReport{Path: s.Path, Reason: s.Reason})
	}
	sort.SliceStable(report, func(i, j int) bool { return report[i].Path < report[j].Path })
	data, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		return err
	}
	if err := writeFile(path, data); err != nil {
		return fmt.Errorf("write skipped report: %w", err)
	}
	return nil
}
