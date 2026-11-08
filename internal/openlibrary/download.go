package openlibrary

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// DefaultURLs point at the rolling OpenLibrary dump files. The server redirects
// these to the dated file of the current month.
var DefaultURLs = map[Kind]string{
	KindEditions: "https://openlibrary.org/data/ol_dump_editions_latest.txt.gz",
	KindWorks:    "https://openlibrary.org/data/ol_dump_works_latest.txt.gz",
	KindAuthors:  "https://openlibrary.org/data/ol_dump_authors_latest.txt.gz",
}

// Downloader fetches missing dump files. Existing valid files are left alone.
type Downloader struct {
	Dir      string
	URLs     map[Kind]string
	Client   *http.Client
	Progress func(kind Kind, done, total int64)
}

// DownloadResult describes one dump after Ensure.
type DownloadResult struct {
	Kind    Kind
	Path    string
	Bytes   int64
	Skipped bool
}

// Ensure downloads the requested dump families that are not already present.
// An interrupted download is kept as a .part file and resumed on the next run.
func (d *Downloader) Ensure(ctx context.Context, kinds []Kind) ([]DownloadResult, error) {
	if d.Dir == "" {
		return nil, errors.New("download directory must not be empty")
	}
	if len(kinds) == 0 {
		kinds = DefaultKinds
	}
	if err := os.MkdirAll(d.Dir, 0o755); err != nil {
		return nil, fmt.Errorf("create dump directory %q: %w", d.Dir, err)
	}
	if d.Client == nil {
		d.Client = defaultClient()
	}
	if d.URLs == nil {
		d.URLs = DefaultURLs
	}

	dataset, _ := LoadDataset(d.Dir)
	results := make([]DownloadResult, 0, len(kinds))
	for _, kind := range kinds {
		if file := existingFile(dataset, kind); file != nil {
			results = append(results, DownloadResult{
				Kind:    kind,
				Path:    file.Path,
				Bytes:   file.Size,
				Skipped: true,
			})
			continue
		}
		result, err := d.download(ctx, kind)
		if err != nil {
			return results, fmt.Errorf("download %s dump: %w", kind, err)
		}
		results = append(results, result)
		dataset, _ = LoadDataset(d.Dir)
	}
	return results, nil
}

func existingFile(dataset *Dataset, kind Kind) *File {
	if dataset == nil {
		return nil
	}
	switch kind {
	case KindEditions:
		return dataset.Editions
	case KindWorks:
		return dataset.Works
	case KindAuthors:
		return dataset.Authors
	default:
		return nil
	}
}

func (d *Downloader) download(ctx context.Context, kind Kind) (DownloadResult, error) {
	if d.Client == nil {
		d.Client = defaultClient()
	}
	rawURL := d.URLs[kind]
	if rawURL == "" {
		return DownloadResult{}, fmt.Errorf("no URL configured for %s dump", kind)
	}

	probe, err := d.probe(ctx, rawURL)
	if err != nil {
		return DownloadResult{}, fmt.Errorf("resolve URL: %w", err)
	}
	target := filepath.Join(d.Dir, targetName(kind, probe.url))
	compressed := strings.HasSuffix(strings.ToLower(target), ".gz")

	if info, err := os.Stat(target); err == nil && !info.IsDir() {
		if validateDump(target, compressed) == nil {
			return DownloadResult{Kind: kind, Path: target, Bytes: info.Size(), Skipped: true}, nil
		}
	}

	part := target + ".part"
	partSize := int64(0)
	if info, err := os.Stat(part); err == nil && !info.IsDir() {
		partSize = info.Size()
	}
	if probe.size > 0 {
		switch {
		case partSize == probe.size:
			return d.finish(kind, part, target, probe.modified, partSize, partSize)
		case partSize > probe.size:
			if err := os.Remove(part); err != nil {
				return DownloadResult{}, fmt.Errorf("remove stale partial file: %w", err)
			}
			partSize = 0
		}
	}

	request, err := http.NewRequestWithContext(ctx, http.MethodGet, probe.url, nil)
	if err != nil {
		return DownloadResult{}, err
	}
	request.Header.Set("Accept-Encoding", "identity")
	request.Header.Set("User-Agent", "margaret-cli")
	if partSize > 0 {
		request.Header.Set("Range", fmt.Sprintf("bytes=%d-", partSize))
	}
	response, err := d.Client.Do(request)
	if err != nil {
		return DownloadResult{}, err
	}
	defer func() {
		_ = response.Body.Close()
	}()

	switch response.StatusCode {
	case http.StatusOK:
		partSize = 0
	case http.StatusPartialContent:
	case http.StatusRequestedRangeNotSatisfiable:
		if err := os.Remove(part); err != nil {
			return DownloadResult{}, fmt.Errorf("remove stale partial file: %w", err)
		}
		return DownloadResult{}, errors.New("server rejected the resume range; run again to restart the download")
	default:
		return DownloadResult{}, fmt.Errorf("unexpected HTTP status %s", response.Status)
	}

	total := probe.size
	if response.StatusCode == http.StatusPartialContent {
		if size := contentRangeTotal(response.Header.Get("Content-Range")); size > 0 {
			total = size
		}
	} else if response.ContentLength >= 0 {
		total = response.ContentLength
	}

	file, err := os.OpenFile(part, os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return DownloadResult{}, err
	}
	if partSize == 0 {
		if err := file.Truncate(0); err != nil {
			_ = file.Close()
			return DownloadResult{}, err
		}
	}
	if _, err := file.Seek(partSize, io.SeekStart); err != nil {
		_ = file.Close()
		return DownloadResult{}, err
	}

	done := partSize
	buffer := make([]byte, 1<<20)
	last := time.Now()
	for {
		n, readErr := response.Body.Read(buffer)
		if n > 0 {
			if _, err := file.Write(buffer[:n]); err != nil {
				_ = file.Close()
				return DownloadResult{}, err
			}
			done += int64(n)
			if d.Progress != nil && (done == total || time.Since(last) >= 250*time.Millisecond) {
				d.Progress(kind, done, total)
				last = time.Now()
			}
		}
		if errors.Is(readErr, io.EOF) {
			break
		}
		if readErr != nil {
			_ = file.Close()
			return DownloadResult{}, readErr
		}
	}
	if err := file.Sync(); err != nil {
		_ = file.Close()
		return DownloadResult{}, err
	}
	if err := file.Close(); err != nil {
		return DownloadResult{}, err
	}
	return d.finish(kind, part, target, probe.modified, done, total)
}

func (d *Downloader) finish(kind Kind, part, target string, modified time.Time, done, total int64) (DownloadResult, error) {
	compressed := strings.HasSuffix(strings.ToLower(target), ".gz")
	if err := validateDump(part, compressed); err != nil {
		_ = os.Remove(part)
		return DownloadResult{}, fmt.Errorf("validate downloaded dump: %w", err)
	}
	if d.Progress != nil {
		d.Progress(kind, done, total)
	}
	if err := os.Rename(part, target); err != nil {
		// Windows does not replace an existing file.
		if removeErr := os.Remove(target); removeErr == nil {
			err = os.Rename(part, target)
		}
		if err != nil {
			return DownloadResult{}, fmt.Errorf("finish download: %w", err)
		}
	}
	if !modified.IsZero() {
		_ = os.Chtimes(target, modified, modified)
	}
	info, err := os.Stat(target)
	if err != nil {
		return DownloadResult{}, err
	}
	return DownloadResult{Kind: kind, Path: target, Bytes: info.Size()}, nil
}

type probeResult struct {
	url      string
	size     int64
	modified time.Time
}

func (d *Downloader) probe(ctx context.Context, rawURL string) (probeResult, error) {
	result, err := d.probeWith(ctx, http.MethodHead, rawURL)
	if err == nil && result.size > 0 {
		return result, nil
	}
	return d.probeWith(ctx, http.MethodGet, rawURL)
}

func (d *Downloader) probeWith(ctx context.Context, method, rawURL string) (probeResult, error) {
	request, err := http.NewRequestWithContext(ctx, method, rawURL, nil)
	if err != nil {
		return probeResult{}, err
	}
	request.Header.Set("Accept-Encoding", "identity")
	request.Header.Set("User-Agent", "margaret-cli")
	if method == http.MethodGet {
		request.Header.Set("Range", "bytes=0-0")
	}
	response, err := d.Client.Do(request)
	if err != nil {
		return probeResult{}, err
	}
	defer func() {
		_ = response.Body.Close()
	}()
	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		return probeResult{}, fmt.Errorf("unexpected HTTP status %s", response.Status)
	}

	result := probeResult{
		url:      response.Request.URL.String(),
		modified: parseHTTPTime(response.Header.Get("Last-Modified")),
	}
	if response.StatusCode == http.StatusPartialContent {
		result.size = contentRangeTotal(response.Header.Get("Content-Range"))
	} else {
		result.size = response.ContentLength
	}
	return result, nil
}

func targetName(kind Kind, finalURL string) string {
	if parsed, err := url.Parse(finalURL); err == nil {
		name := path.Base(parsed.Path)
		if parsedKind, _, _, ok := parseDumpName(name); ok && parsedKind == kind {
			return name
		}
	}
	return fmt.Sprintf("ol_dump_%s_latest.txt.gz", kind)
}

func contentRangeTotal(value string) int64 {
	_, total, ok := strings.Cut(value, "/")
	if !ok || total == "*" {
		return 0
	}
	n, err := strconv.ParseInt(total, 10, 64)
	if err != nil {
		return 0
	}
	return n
}

func parseHTTPTime(value string) time.Time {
	if value == "" {
		return time.Time{}
	}
	parsed, err := http.ParseTime(value)
	if err != nil {
		return time.Time{}
	}
	return parsed
}

func defaultClient() *http.Client {
	transport, ok := http.DefaultTransport.(*http.Transport)
	if !ok {
		return &http.Client{Transport: &http.Transport{DisableCompression: true}}
	}
	cloned := transport.Clone()
	cloned.DisableCompression = true
	return &http.Client{Transport: cloned}
}
