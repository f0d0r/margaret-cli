package parser

import (
	"slices"
	"testing"

	"github.com/f0d0r/margaret-cli/internal/langid"
)

func TestApplyDetectedLanguage(t *testing.T) {
	high := langid.Result{Code: "hu", Confidence: 0.95}
	medium := langid.Result{Code: "hu", Confidence: 0.6}
	for _, tc := range []struct {
		name      string
		languages []string
		detected  langid.Result
		ok        bool
		want      []string
	}{
		{"empty list is filled", nil, high, true, []string{"hu"}},
		{"confident mismatch is corrected", []string{"en"}, high, true, []string{"hu"}},
		{"weak mismatch keeps declared", []string{"en"}, medium, true, []string{"en"}},
		{"match keeps regional tag", []string{"hu-HU"}, high, true, []string{"hu-HU"}},
		{"underscore tag matches", []string{"hu_HU"}, high, true, []string{"hu_HU"}},
		{"multilingual declaration containing detection is kept", []string{"en", "hu"}, high, true, []string{"en", "hu"}},
		{"confident mismatch replaces multilingual declaration", []string{"en", "de"}, high, true, []string{"hu"}},
		{"no detection keeps declared", []string{"en"}, langid.Result{}, false, []string{"en"}},
		{"low confidence keeps declared", []string{"en"}, langid.Result{Code: "hu", Confidence: 0.4}, true, []string{"en"}},
		{"no detection and no declaration stays empty", nil, langid.Result{}, false, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := ApplyDetectedLanguage(tc.languages, tc.detected, tc.ok)
			if !slices.Equal(got, tc.want) {
				t.Errorf("ApplyDetectedLanguage(%v, %+v, %v) = %v, want %v",
					tc.languages, tc.detected, tc.ok, got, tc.want)
			}
		})
	}
}
