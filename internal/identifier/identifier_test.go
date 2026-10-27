package identifier

import "testing"

func TestMergeSources(t *testing.T) {
	for _, tc := range []struct {
		name string
		a    Source
		b    Source
		want Source
	}{
		{"same metadata", SourceMetadata, SourceMetadata, SourceMetadata},
		{"same text", SourceText, SourceText, SourceText},
		{"metadata and text", SourceMetadata, SourceText, SourceMetadataAndText},
		{"text and metadata", SourceText, SourceMetadata, SourceMetadataAndText},
		{"already both", SourceMetadataAndText, SourceText, SourceMetadataAndText},
		{"empty defaults metadata", "", SourceText, SourceMetadataAndText},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := MergeSources(tc.a, tc.b); got != tc.want {
				t.Errorf("MergeSources(%q, %q) = %q, want %q", tc.a, tc.b, got, tc.want)
			}
		})
	}
}
