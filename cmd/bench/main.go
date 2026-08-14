// Command bench generates a synthetic ebook dataset and parses strace I/O
// logs, so the margaret-tools scanner can be benchmarked with and without
// temp-file spooling.
//
// Usage:
//
//	bench gen -out <dir>            generate a dataset directory
//	bench report <strace.log> <dir>  summarize I/O bytes by category
package main

import (
	"archive/tar"
	"archive/zip"
	"bufio"
	"compress/gzip"
	"encoding/binary"
	"flag"
	"fmt"
	"io"
	"math/rand"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
)

func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(2)
	}
	switch os.Args[1] {
	case "gen":
		gen(os.Args[2:])
	case "report":
		report(os.Args[2:])
	default:
		usage()
		os.Exit(2)
	}
}

func usage() {
	fmt.Fprintln(os.Stderr, "usage: bench gen -out <dir> | bench report <strace.log> <dir>")
}

// ---------------------------------------------------------------------------
// Dataset generation
// ---------------------------------------------------------------------------

type book struct {
	name   string
	format string // "epub" or "mobi"
	size   int    // target size in bytes
	title  string
	author string
}

// dataset returns a fixed, deterministic set of books.
func dataset() []book {
	var books []book
	sizes := []int{1 << 20, 2 << 20, 4 << 20, 6 << 20, 8 << 20}
	titles := []string{"The Long Voyage", "Midnight Equations", "The Quiet Forge",
		"Atlas of Small Islands", "A Winter in Rijeka", "Fields of Static",
		"The Cartographer's Daughter", "Static Bloom", "Paper Moons", "The Salt Library"}
	authors := []string{"Jane Doe", "John Smith", "A. Novák", "Kata Tóth", "Erin Vale"}
	for i := 0; i < 20; i++ {
		books = append(books, book{
			name:   fmt.Sprintf("b%02d", i),
			format: "epub",
			size:   sizes[i%len(sizes)],
			title:  titles[i%len(titles)],
			author: authors[i%len(authors)],
		})
	}
	for i := 0; i < 15; i++ {
		books = append(books, book{
			name:   fmt.Sprintf("m%02d", i),
			format: "mobi",
			size:   sizes[(i+1)%len(sizes)],
			title:  titles[(i+3)%len(titles)],
			author: authors[(i+2)%len(authors)],
		})
	}
	return books
}

func gen(args []string) {
	fs := flag.NewFlagSet("gen", flag.ExitOnError)
	out := fs.String("out", "", "output directory")
	_ = fs.Parse(args)
	if *out == "" {
		fs.Usage()
		os.Exit(2)
	}
	if err := os.RemoveAll(*out); err != nil {
		fatal(err)
	}
	if err := os.MkdirAll(*out, 0o755); err != nil {
		fatal(err)
	}

	books := dataset()
	// Deterministic, but distinct content per book.
	rng := rand.New(rand.NewSource(42))

	var loose []book
	var zipBooks, tarBooks, rarBooks, sz7Books, gzBooks []book
	for i, b := range books {
		switch i % 7 {
		case 0:
			loose = append(loose, b)
		case 1:
			zipBooks = append(zipBooks, b)
		case 2:
			tarBooks = append(tarBooks, b)
		case 3:
			gzBooks = append(gzBooks, b)
		case 4:
			rarBooks = append(rarBooks, b)
		case 5:
			sz7Books = append(sz7Books, b)
		case 6:
			loose = append(loose, b) // extra loose
		}
	}

	dir := *out

	// Loose books.
	looseDir := filepath.Join(dir, "loose")
	mustMkdir(looseDir)
	for i, b := range loose {
		p := filepath.Join(looseDir, b.name+"."+b.format)
		if err := writeBook(p, b, rng); err != nil {
			fatal(err)
		}
		_ = i
	}

	// Non-ebook noise files that the scanner must skip.
	noise := filepath.Join(dir, "noise")
	mustMkdir(noise)
	for _, n := range []struct {
		name, content string
	}{
		{"notes.txt", "some plain text that is not an ebook"},
		{"manual.pdf", "\x25PDF-1.7 not a real book"},
		{"cover.png", "\x89PNG\r\n\x1a\n"},
	} {
		if err := os.WriteFile(filepath.Join(noise, n.name), []byte(n.content), 0o644); err != nil {
			fatal(err)
		}
	}

	// zip archive.
	writeZipArchives(dir, zipBooks, rng)

	// tar archive.
	writeTarArchive(dir, tarBooks, rng)

	// gz single-stream books.
	gzDir := filepath.Join(dir, "gz")
	mustMkdir(gzDir)
	for i, b := range gzBooks {
		p := filepath.Join(gzDir, b.name+"."+b.format+".gz")
		if err := writeGz(p, b, rng); err != nil {
			fatal(err)
		}
		_ = i
	}

	// rar and 7z archives (need the command-line tools).
	writeRarArchive(dir, rarBooks, rng)
	write7zArchive(dir, sz7Books, rng)

	// Nested archive: outer.zip containing inner.zip (with books) + loose books.
	writeNested(dir, rng)

	fmt.Printf("generated dataset in %s (%d books)\n", dir, len(books))
}

func mustMkdir(p string) {
	if err := os.MkdirAll(p, 0o755); err != nil {
		fatal(err)
	}
}

func fatal(err error) {
	fmt.Fprintln(os.Stderr, "bench:", err)
	os.Exit(1)
}

func writeBook(path string, b book, rng *rand.Rand) error {
	if b.format == "epub" {
		return writeEpub(path, b, rng)
	}
	return writeMobi(path, b, rng)
}

// fill appends pseudo-random, mostly incompressible bytes to w up to n total.
func fill(w io.Writer, n int, rng *rand.Rand) error {
	buf := make([]byte, 64*1024)
	for written := 0; written < n; {
		chunk := min(len(buf), n-written)
		if _, err := rng.Read(buf[:chunk]); err != nil {
			return err
		}
		if _, err := w.Write(buf[:chunk]); err != nil {
			return err
		}
		written += chunk
	}
	return nil
}

func writeEpub(path string, b book, rng *rand.Rand) error {
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	zw := zip.NewWriter(f)

	mimetype, err := zw.CreateHeader(&zip.FileHeader{Name: "mimetype", Method: zip.Store})
	if err != nil {
		return err
	}
	if _, err := mimetype.Write([]byte("application/epub+zip")); err != nil {
		return err
	}

	container := `<?xml version="1.0" encoding="UTF-8"?>
<container xmlns="urn:oasis:names:tc:opendocument:xmlns:container" version="1.0">
  <rootfiles><rootfile full-path="OEBPS/content.opf" media-type="application/oebps-package+xml"/></rootfiles>
</container>`
	if err := putZip(zw, "META-INF/container.xml", []byte(container)); err != nil {
		return err
	}

	opf := fmt.Sprintf(`<?xml version="1.0" encoding="UTF-8"?>
<package xmlns="http://www.idpf.org/2007/opf" version="3.0">
  <metadata xmlns:dc="http://purl.org/dc/elements/1.1/">
    <dc:title>%s</dc:title>
    <dc:creator>%s</dc:creator>
  </metadata>
  <manifest>
    <item id="c1" href="chapter1.xhtml" media-type="application/xhtml+xml"/>
  </manifest>
  <spine><itemref idref="c1"/></spine>
</package>`, b.title, b.author)
	if err := putZip(zw, "OEBPS/content.opf", []byte(opf)); err != nil {
		return err
	}

	// Filler chapter that dominates the file size.
	cw, err := zw.CreateHeader(&zip.FileHeader{Name: "OEBPS/chapter1.xhtml", Method: zip.Deflate})
	if err != nil {
		return err
	}
	head := []byte("<html><body><p>")
	if _, err := cw.Write(head); err != nil {
		return err
	}
	if err := fill(cw, b.size-len(head)-7, rng); err != nil {
		return err
	}
	if _, err := cw.Write([]byte("</p></body></html>")); err != nil {
		return err
	}

	if err := zw.Close(); err != nil {
		return err
	}
	return f.Close()
}

func putZip(zw *zip.Writer, name string, data []byte) error {
	w, err := zw.CreateHeader(&zip.FileHeader{Name: name, Method: zip.Deflate})
	if err != nil {
		return err
	}
	_, err = w.Write(data)
	return err
}

const (
	pdbHeaderSize   = 78
	palmDocHeadSize = 16
)

// writeMobi writes a valid, parseable MOBI (PDB) file of roughly b.size bytes.
func writeMobi(path string, b book, rng *rand.Rand) error {
	// Record 0 = MOBI header (248 bytes) + EXTH block.
	mobiHeader := make([]byte, 248)
	binary.BigEndian.PutUint16(mobiHeader[0:2], 1)      // compression: none
	binary.BigEndian.PutUint32(mobiHeader[4:8], 1000)   // text length
	binary.BigEndian.PutUint16(mobiHeader[8:10], 1)     // text record count
	binary.BigEndian.PutUint16(mobiHeader[10:12], 4096) // record size
	binary.BigEndian.PutUint16(mobiHeader[12:14], 0)    // encryption: none
	copy(mobiHeader[16:20], "MOBI")
	binary.BigEndian.PutUint32(mobiHeader[20:24], 232)    // header length
	binary.BigEndian.PutUint32(mobiHeader[24:28], 2)      // mobi type: book
	binary.BigEndian.PutUint32(mobiHeader[28:32], 65001)  // UTF-8
	binary.BigEndian.PutUint32(mobiHeader[36:40], 6)      // version
	binary.BigEndian.PutUint32(mobiHeader[80:84], 1)      // first non-text record
	binary.BigEndian.PutUint32(mobiHeader[92:96], 1033)   // locale en-US
	binary.BigEndian.PutUint32(mobiHeader[128:132], 0x40) // EXTH present

	// EXTH block with title (503) and author (100) records.
	exth := buildExth([][2][]byte{
		{uint32Bytes(503), []byte(b.title)},
		{uint32Bytes(100), []byte(b.author)},
	})

	record0 := append(append([]byte{}, mobiHeader...), exth...)

	// Filler text records to reach the target size.
	const recordSize = 4096
	fillerRecords := (b.size - len(record0)) / recordSize
	if fillerRecords < 1 {
		fillerRecords = 1
	}
	numRecords := 1 + fillerRecords

	buf := make([]byte, 0, pdbHeaderSize+8*numRecords+len(record0)+fillerRecords*recordSize)

	// PDB header.
	header := make([]byte, pdbHeaderSize)
	copy(header[0:], b.name)
	binary.BigEndian.PutUint32(header[48:52], 1) // modification number
	copy(header[60:64], "BOOK")
	copy(header[64:68], "MOBI")
	binary.BigEndian.PutUint32(header[68:72], 1) // unique id seed
	binary.BigEndian.PutUint16(header[76:78], uint16(numRecords))
	buf = append(buf, header...)

	// Record info table. Compute offsets as we append records.
	offset := pdbHeaderSize + 8*numRecords
	recordOffsets := []int{offset}
	recordData := [][]byte{record0}
	text := make([]byte, recordSize)
	for i := 0; i < fillerRecords; i++ {
		if _, err := rng.Read(text); err != nil {
			return err
		}
		recordData = append(recordData, append([]byte{}, text...))
		offset += recordSize
		recordOffsets = append(recordOffsets, offset)
	}
	for _, o := range recordOffsets {
		info := make([]byte, 8)
		binary.BigEndian.PutUint32(info[0:4], uint32(o))
		buf = append(buf, info...)
	}
	for _, d := range recordData {
		buf = append(buf, d...)
	}

	return os.WriteFile(path, buf, 0o644)
}

func uint32Bytes(v uint32) []byte {
	b := make([]byte, 4)
	binary.BigEndian.PutUint32(b, v)
	return b
}

// buildExth builds an EXTH block: magic + header length + record count + records.
func buildExth(records [][2][]byte) []byte {
	var body []byte
	for _, r := range records {
		rec := make([]byte, 8+len(r[1]))
		copy(rec[0:4], r[0])
		binary.BigEndian.PutUint32(rec[4:8], uint32(8+len(r[1])))
		copy(rec[8:], r[1])
		body = append(body, rec...)
	}
	exth := make([]byte, 12+len(body))
	copy(exth[0:4], "EXTH")
	binary.BigEndian.PutUint32(exth[4:8], uint32(12+len(body)))
	binary.BigEndian.PutUint32(exth[8:12], uint32(len(records)))
	copy(exth[12:], body)
	return exth
}

func writeZipArchives(dir string, books []book, rng *rand.Rand) {
	// Split into two archives so there is more than one file.
	half := len(books) / 2
	names := []string{"books-a.zip", "books-b.zip"}
	splits := [][]book{books[:half], books[half:]}
	for i, name := range names {
		if len(splits[i]) == 0 {
			continue
		}
		p := filepath.Join(dir, name)
		if err := writeZip(p, splits[i], rng); err != nil {
			fatal(err)
		}
	}
}

func writeZip(path string, books []book, rng *rand.Rand) error {
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	zw := zip.NewWriter(f)
	for _, b := range books {
		// Store an in-memory ebook; sizes are a few MB, fine for the dataset.
		inner, err := bookBytes(b, rng)
		if err != nil {
			return err
		}
		if err := putZip(zw, b.name+"."+b.format, inner); err != nil {
			return err
		}
	}
	if err := zw.Close(); err != nil {
		return err
	}
	return f.Close()
}

func writeTarArchive(dir string, books []book, rng *rand.Rand) {
	f, err := os.Create(filepath.Join(dir, "books.tar"))
	if err != nil {
		fatal(err)
	}
	tw := tar.NewWriter(f)
	for _, b := range books {
		inner, err := bookBytes(b, rng)
		if err != nil {
			fatal(err)
		}
		if err := tw.WriteHeader(&tar.Header{Name: b.name + "." + b.format, Mode: 0o644, Size: int64(len(inner))}); err != nil {
			fatal(err)
		}
		if _, err := tw.Write(inner); err != nil {
			fatal(err)
		}
	}
	if err := tw.Close(); err != nil {
		fatal(err)
	}
	if err := f.Close(); err != nil {
		fatal(err)
	}
}

func writeGz(path string, b book, rng *rand.Rand) error {
	inner, err := bookBytes(b, rng)
	if err != nil {
		return err
	}
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	gw := gzip.NewWriter(f)
	if _, err := gw.Write(inner); err != nil {
		return err
	}
	if err := gw.Close(); err != nil {
		return err
	}
	return f.Close()
}

// bookBytes renders a book into an in-memory buffer.
func bookBytes(b book, rng *rand.Rand) ([]byte, error) {
	tmp, err := os.CreateTemp("", "bench-book-*")
	if err != nil {
		return nil, err
	}
	defer func() {
		_ = tmp.Close()
		_ = os.Remove(tmp.Name())
	}()
	if err := writeBook(tmp.Name(), b, rng); err != nil {
		return nil, err
	}
	return os.ReadFile(tmp.Name())
}

func writeRarArchive(dir string, books []book, rng *rand.Rand) {
	stage := filepath.Join(dir, ".rar-stage")
	mustMkdir(stage)
	for _, b := range books {
		p := filepath.Join(stage, b.name+"."+b.format)
		if err := writeBook(p, b, rng); err != nil {
			fatal(err)
		}
	}
	out := filepath.Join(dir, "books.rar")
	cmd := exec.Command("rar", "a", "-m5", out, filepath.Join(stage, "*"))
	if combined, err := cmd.CombinedOutput(); err != nil {
		fmt.Fprintf(os.Stderr, "bench: rar unavailable (%v): %s\n", err, combined)
		_ = os.Remove(out) // best effort
		return
	}
	_ = os.RemoveAll(stage)
}

func write7zArchive(dir string, books []book, rng *rand.Rand) {
	stage := filepath.Join(dir, ".7z-stage")
	mustMkdir(stage)
	for _, b := range books {
		p := filepath.Join(stage, b.name+"."+b.format)
		if err := writeBook(p, b, rng); err != nil {
			fatal(err)
		}
	}
	out := filepath.Join(dir, "books.7z")
	cmd := exec.Command("7z", "a", "-t7z", "-mx=5", out, filepath.Join(stage, "*"))
	if res, err := cmd.CombinedOutput(); err != nil {
		fmt.Fprintf(os.Stderr, "bench: 7z unavailable (%v): %s\n", err, res)
		_ = os.Remove(out)
		return
	}
	_ = os.RemoveAll(stage)
}

func writeNested(dir string, rng *rand.Rand) {
	// inner.zip with a few books.
	var inner []book
	all := dataset()
	for i := 0; i < len(all); i += 13 {
		inner = append(inner, all[i])
	}
	if len(inner) > 3 {
		inner = inner[:3]
	}
	innerZip := filepath.Join(dir, ".nested-stage", "inner.zip")
	mustMkdir(filepath.Dir(innerZip))
	if err := writeZip(innerZip, inner, rng); err != nil {
		fatal(err)
	}

	// outer.zip: the inner.zip plus one loose book.
	var outer []book
	outer = append(outer, inner...)
	// One loose book on its own.
	var looseBook book
	for _, b := range all {
		if b.format == "epub" {
			looseBook = b
			break
		}
	}
	outerPath := filepath.Join(dir, "nested.zip")
	f, err := os.Create(outerPath)
	if err != nil {
		fatal(err)
	}
	zw := zip.NewWriter(f)
	innerBytes, err := os.ReadFile(innerZip)
	if err != nil {
		fatal(err)
	}
	if err := putZip(zw, "inner.zip", innerBytes); err != nil {
		fatal(err)
	}
	lb, err := bookBytes(looseBook, rng)
	if err != nil {
		fatal(err)
	}
	if err := putZip(zw, looseBook.name+"."+looseBook.format, lb); err != nil {
		fatal(err)
	}
	if err := zw.Close(); err != nil {
		fatal(err)
	}
	if err := f.Close(); err != nil {
		fatal(err)
	}
	_ = os.RemoveAll(filepath.Join(dir, ".nested-stage"))
}

// ---------------------------------------------------------------------------
// strace report parsing
// ---------------------------------------------------------------------------

type ioStats struct {
	DatasetRead int64
	TempRead    int64
	TempWrite   int64
	OtherRead   int64
	OtherWrite  int64
}

var syscallRe = regexp.MustCompile(`^(?:\d+\s+)?([a-z0-9_]+)\((\d+)(?:<([^>]*)>)?,([^\n]*) = (\d+|-1)`)

func report(args []string) {
	if len(args) < 2 {
		usage()
		os.Exit(2)
	}
	logPrefix, dataDir := args[0], args[1]

	dataAbs, err := filepath.Abs(dataDir)
	if err != nil {
		fatal(err)
	}

	var stats ioStats
	if err := filepath.WalkDir(filepath.Dir(logPrefix), func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		// Match the prefix itself or a per-thread file "<prefix>.<tid>".
		if path != logPrefix && !strings.HasPrefix(filepath.Base(path), filepath.Base(logPrefix)+".") {
			return nil
		}
		return parseStraceFile(path, dataAbs, &stats)
	}); err != nil {
		fatal(err)
	}

	mb := func(v int64) float64 { return float64(v) / (1 << 20) }
	fmt.Printf("dataset-read (network proxy): %10.2f MiB\n", mb(stats.DatasetRead))
	fmt.Printf("temp-write   (spooled to disk):%10.2f MiB\n", mb(stats.TempWrite))
	fmt.Printf("temp-read    (spool re-read):  %10.2f MiB\n", mb(stats.TempRead))
	fmt.Printf("other-read:                    %10.2f MiB\n", mb(stats.OtherRead))
	fmt.Printf("other-write:                   %10.2f MiB\n", mb(stats.OtherWrite))
}

func parseStraceFile(path, dataDir string, stats *ioStats) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()

	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 1<<20), 1<<20)
	for sc.Scan() {
		line := sc.Text()
		m := syscallRe.FindStringSubmatch(line)
		if m == nil {
			continue
		}
		sys, fdPath := m[1], m[3]
		ret, err := strconv.ParseInt(m[5], 10, 64)
		if err != nil || ret < 0 {
			continue
		}
		isWrite := sys == "write" || sys == "pwrite64"
		isRead := sys == "read" || sys == "pread64"
		if !isRead && !isWrite {
			continue
		}
		switch classify(fdPath, dataDir) {
		case "dataset":
			if isRead {
				stats.DatasetRead += ret
			} else {
				stats.OtherWrite += ret // writes into the dataset are unexpected
			}
		case "temp":
			if isRead {
				stats.TempRead += ret
			} else {
				stats.TempWrite += ret
			}
		default:
			if isRead {
				stats.OtherRead += ret
			} else {
				stats.OtherWrite += ret
			}
		}
	}
	return sc.Err()
}

func classify(fdPath, dataDir string) string {
	if fdPath == "" {
		return "other"
	}
	if strings.Contains(fdPath, "margaret-ebook-") || strings.Contains(fdPath, "margaret-archive-") {
		return "temp"
	}
	if strings.HasPrefix(fdPath, dataDir) {
		return "dataset"
	}
	return "other"
}
