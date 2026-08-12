package parser

import (
	"context"
	"strings"
	"testing"
)

func TestDummy(t *testing.T) {
	md, err := Dummy{}.Parse(context.Background(), strings.NewReader("x"), "epub")
	if err != nil {
		t.Fatal(err)
	}
	if md.Author != "" || md.Title != "" {
		t.Fatalf("dummy parser should return empty metadata, got %+v", md)
	}
}
