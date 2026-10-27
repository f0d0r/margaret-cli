package report

import (
	"fmt"
	"slices"
	"testing"
	"time"

	"github.com/f0d0r/margaret-cli/internal/identifier"
)

func TestNormalizePublicationDate(t *testing.T) {
	for _, tc := range []struct {
		name string
		in   string
		want string
	}{
		{"empty", "", ""},
		{"year", "1999", "1999"},
		{"year with spaces", "  1999  ", "1999"},
		{"year and month", "1999-05", "1999-05"},
		{"full date", "1999-05-04", "1999-05-04"},
		{"rfc3339", "1999-05-04T00:00:00Z", "1999-05-04"},
		{"rfc3339 offset", "1999-05-04T00:00:00+02:00", "1999-05-04"},
		{"iso datetime", "1999-05-04T00:00:00", "1999-05-04"},
		{"space datetime", "1999-05-04 00:00:00", "1999-05-04"},
		{"approximate", "circa 1999", ""},
		{"localized", "1999. május", ""},
		{"impossible day", "1999-02-30", ""},
		{"impossible month", "1999-13", ""},
		{"zero year", "0000", ""},
		{"too early day", "0101-01-01", ""},
		{"too early year", "0999", ""},
		{"next year", fmt.Sprintf("%d", time.Now().Year()+1), fmt.Sprintf("%d", time.Now().Year()+1)},
		{"future year", fmt.Sprintf("%d", time.Now().Year()+2), ""},
		{"garbage", "not a date", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := normalizePublicationDate(tc.in); got != tc.want {
				t.Errorf("normalizePublicationDate(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

func TestNormalizeLanguage(t *testing.T) {
	for _, tc := range []struct {
		name string
		in   string
		want string
		ok   bool
	}{
		{"base", "en", "en", true},
		{"upper case", "EN", "en", true},
		{"region", "en-US", "en-US", true},
		{"underscore", "en_US", "en-US", true},
		{"german", "de-DE", "de-DE", true},
		{"empty", "", "", false},
		{"invalid name", "english", "", false},
		{"undetermined", "und", "", false},
		{"multiple", "mul", "", false},
		{"no linguistic content", "zxx", "", false},
		{"uncoded", "mis", "", false},
		{"undetermined with region", "und-US", "", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := normalizeLanguage(tc.in)
			if got != tc.want || ok != tc.ok {
				t.Errorf("normalizeLanguage(%q) = (%q, %v), want (%q, %v)", tc.in, got, ok, tc.want, tc.ok)
			}
		})
	}
}

func TestMergeIdentifiers(t *testing.T) {
	got := mergeIdentifiers([]BookIdentifier{
		{Scheme: "ISBN", Value: "0-306-40615-2", Source: identifier.SourceMetadata},
		{Scheme: "ISBN-13", Value: "978-0-306-40615-7", Source: identifier.SourceText},
		{Scheme: "isbn", Value: "9780375704024", Source: identifier.SourceText},
		{Scheme: "ISBN-13", Value: "9780375704024", Source: identifier.SourceText},
		{Scheme: "ISBN-10", Value: "0-306-40615-2", Source: identifier.SourceMetadata},
		{Scheme: "ISBN", Value: "9798400000003", Source: identifier.SourceMetadata},
		{Scheme: "DOI", Value: "10.1000/test", Source: identifier.SourceMetadata},
		{Scheme: "", Value: "9780306406157", Source: identifier.SourceMetadata},
		{Scheme: "ISBN", Value: "9780306406158", Source: identifier.SourceMetadata},
	})
	want := []BookIdentifier{
		{Scheme: "ISBN", Value: "9780306406157", Source: identifier.SourceMetadataAndText},
		{Scheme: "ISBN", Value: "9780375704024", Source: identifier.SourceText},
		{Scheme: "ISBN", Value: "9798400000003", Source: identifier.SourceMetadata},
	}
	if !slices.Equal(got, want) {
		t.Errorf("mergeIdentifiers() = %v, want %v", got, want)
	}
}

func TestMergeBookMetadata(t *testing.T) {
	got := mergeBookMetadata(
		[]fileMetadata{
			{
				description:     "<p>Short</p>",
				publisher:       "Penguin",
				publicationDate: "1999-05-04T00:00:00Z",
				series:          "Saga",
				seriesIndex:     "1",
			},
			{
				description:     "<div>A much longer description</div>",
				publisher:       "Penguin",
				publicationDate: "1999",
				series:          "Saga",
				seriesIndex:     "2",
			},
			{
				description:     "Third",
				publisher:       "Other",
				publicationDate: "2000",
				series:          "Other",
				seriesIndex:     "9",
			},
		},
		[]string{"Fiction", "Adventure", "fiction", "Science"},
		[]string{"en", "en-US", "english", "und"},
		[]BookIdentifier{
			{Scheme: "ISBN", Value: "9780306406157", Source: identifier.SourceMetadata},
			{Scheme: "DOI", Value: "10.1000/test", Source: identifier.SourceMetadata},
			{Scheme: "ISBN", Value: "9780306406157", Source: identifier.SourceMetadata},
		},
	)
	if got.description != "A much longer description" {
		t.Errorf("description = %q", got.description)
	}
	if got.series != "Saga" || got.seriesIndex != "1" {
		t.Errorf("series = %q, index = %q", got.series, got.seriesIndex)
	}
	if want := []string{"Fiction", "Adventure", "Science"}; !slices.Equal(got.subjects, want) {
		t.Errorf("subjects = %v, want %v", got.subjects, want)
	}
	if want := []string{"en", "en-US"}; !slices.Equal(got.languages, want) {
		t.Errorf("languages = %v, want %v", got.languages, want)
	}
	if want := []BookIdentifier{{Scheme: "ISBN", Value: "9780306406157", Source: identifier.SourceMetadata}}; !slices.Equal(got.identifiers, want) {
		t.Errorf("identifiers = %v, want %v", got.identifiers, want)
	}
	if want := []BookPublication{
		{Publisher: "Penguin", PublicationDate: "1999-05-04"},
		{Publisher: "Penguin", PublicationDate: "1999"},
		{Publisher: "Other", PublicationDate: "2000"},
	}; !slices.Equal(got.publications, want) {
		t.Errorf("publications = %v, want %v", got.publications, want)
	}
}

func TestMergePublications(t *testing.T) {
	got := mergePublications([]fileMetadata{
		{publisher: "Penguin"},
		{publisher: "penguin", publicationDate: "1999"},
		{publisher: "PENGUIN", publicationDate: "1999-05-04"},
		{publisher: "Penguin", publicationDate: "1999"},
		{publisher: "Other"},
		{publicationDate: "1998"},
		{publicationDate: "1998"},
		{},
	})
	want := []BookPublication{
		{Publisher: "Penguin", PublicationDate: "1999"},
		{Publisher: "Penguin", PublicationDate: "1999-05-04"},
		{Publisher: "Other"},
		{PublicationDate: "1998"},
	}
	if !slices.Equal(got, want) {
		t.Errorf("mergePublications() = %v, want %v", got, want)
	}
}
