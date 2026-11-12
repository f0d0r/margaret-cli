package tempfile

import (
	"io"
	"os"
	"testing"
)

func TestNewUnlinksFile(t *testing.T) {
	temp, err := New("margaret-tempfile-test-*.tmp")
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		_ = temp.Close()
	}()
	if _, err := os.Stat(temp.Name()); !os.IsNotExist(err) {
		t.Fatalf("temporary file still exists at %q: %v", temp.Name(), err)
	}
}

func TestReadWrite(t *testing.T) {
	temp, err := New("margaret-tempfile-test-*.tmp")
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		_ = temp.Close()
	}()
	if _, err := temp.File().WriteString("hello"); err != nil {
		t.Fatal(err)
	}
	reader, err := temp.Reader()
	if err != nil {
		t.Fatal(err)
	}
	data, err := io.ReadAll(reader)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "hello" {
		t.Fatalf("data = %q, want hello", data)
	}
	// Reading again must not be affected by the previous reader.
	reader, err = temp.Reader()
	if err != nil {
		t.Fatal(err)
	}
	data, err = io.ReadAll(reader)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "hello" {
		t.Fatalf("second read = %q, want hello", data)
	}
}

func TestCloseTwice(t *testing.T) {
	temp, err := New("margaret-tempfile-test-*.tmp")
	if err != nil {
		t.Fatal(err)
	}
	if err := temp.Close(); err != nil {
		t.Fatal(err)
	}
	if err := temp.Close(); err != nil {
		t.Fatalf("second close: %v", err)
	}
}
