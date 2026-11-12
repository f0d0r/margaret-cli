package matcher

import (
	"strings"

	"github.com/f0d0r/margaret-cli/internal/openlibrary"
)

// Version identifies the matching and scoring rules. Bump it whenever a
// change can alter match results, so stored matches are reprocessed.
const Version = "7"

// DatasetVersion returns the dump version string used to decide whether a
// stored match is still current.
func DatasetVersion(dataset *openlibrary.Dataset) string {
	if dataset == nil {
		return ""
	}
	if dataset.EditionsFromCombined() {
		return "combined=" + fileVersion(dataset.Combined)
	}
	var parts []string
	if dataset.Editions != nil {
		parts = append(parts, "editions="+fileVersion(dataset.Editions))
	}
	if dataset.Works != nil {
		parts = append(parts, "works="+fileVersion(dataset.Works))
	}
	if dataset.Authors != nil {
		parts = append(parts, "authors="+fileVersion(dataset.Authors))
	}
	return strings.Join(parts, ";")
}

func fileVersion(file *openlibrary.File) string {
	if file == nil {
		return ""
	}
	if file.Version != "" && file.Version != "latest" {
		return file.Version
	}
	if date := file.Date(); !date.IsZero() {
		return date.Format("2006-01-02")
	}
	return "unknown"
}
