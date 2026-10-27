package report

import "testing"

func TestPlainText(t *testing.T) {
	for _, tc := range []struct {
		name string
		in   string
		want string
	}{
		{"plain", "A plain description", "A plain description"},
		{"tags", "This is <b>bold</b> text", "This is bold text"},
		{"paragraphs", "<p>First.</p><p>Second.</p>", "First. Second."},
		{"line break", "First.<br>Second.", "First. Second."},
		{"entities", "Pride &amp; Prejudice", "Pride & Prejudice"},
		{"script and style", "<script>bad()</script><style>bad{}</style><p>Good</p>", "Good"},
		{"head", "<head><title>Bad</title></head><body>Good</body>", "Good"},
		{"malformed", "<p>Hello", "Hello"},
		{"empty", "", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := plainText(tc.in); got != tc.want {
				t.Errorf("plainText(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}
