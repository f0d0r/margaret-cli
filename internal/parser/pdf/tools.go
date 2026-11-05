// Package pdf extracts metadata and plain text from PDF documents using the
// Poppler command-line tools.
package pdf

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
)

// Tools holds the Poppler binaries used to read a PDF. Both are required:
// pdftotext extracts the text, pdfinfo reads the Info dictionary and XMP
// metadata.
type Tools struct {
	Text string
	Info string
}

// Available reports whether both tools are configured.
func (t Tools) Available() bool {
	return t.Text != "" && t.Info != ""
}

// ErrToolsUnavailable reports that PDF support is disabled because pdftotext
// or pdfinfo is missing.
var ErrToolsUnavailable = errors.New("pdf: pdftotext and pdfinfo are required")

// DetectTools locates pdftotext and pdfinfo. An empty dir searches PATH;
// otherwise both binaries are looked up in dir. The returned error lists the
// missing tools.
func DetectTools(dir string) (Tools, error) {
	text, textErr := findTool(dir, "pdftotext")
	info, infoErr := findTool(dir, "pdfinfo")
	if textErr == nil && infoErr == nil {
		return Tools{Text: text, Info: info}, nil
	}

	var missing []string
	if textErr != nil {
		missing = append(missing, "pdftotext")
	}
	if infoErr != nil {
		missing = append(missing, "pdfinfo")
	}
	where := "PATH"
	if dir != "" {
		where = dir
	}
	return Tools{}, fmt.Errorf("%s not found in %s", strings.Join(missing, " and "), where)
}

// findTool looks up one Poppler binary and returns its path.
func findTool(dir, name string) (string, error) {
	if dir == "" {
		return exec.LookPath(name)
	}
	candidates := []string{name}
	if runtime.GOOS == "windows" {
		candidates = append(candidates, name+".exe")
	}
	for _, candidate := range candidates {
		path := filepath.Join(dir, candidate)
		info, err := os.Stat(path)
		if err == nil && !info.IsDir() {
			return path, nil
		}
	}
	return "", fmt.Errorf("%s not found in %s", name, dir)
}
