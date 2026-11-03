package txt

import "testing"

func TestIsJunkContent(t *testing.T) {
	cases := []struct {
		name string
		head string
		size int64
		want bool
	}{
		{
			name: "html",
			head: "<!DOCTYPE html>\n<html><body><h1>Book</h1></body></html>\n",
			size: 60,
			want: true,
		},
		{
			name: "http cache",
			head: "HTTP/1.1 200 OK\nContent-Type: text/html\nContent-Length: 42\n\n<html></html>\n",
			size: 80,
			want: true,
		},
		{
			name: "credits only",
			head: "Digitalizálta: Kovács Béla\nKorrektúra: Tóth Anna\nMEK\n",
			size: 55,
			want: true,
		},
		{
			name: "blurb only",
			head: "Fülszöveg\nEgy rövid leírás a könyvről, ami nem maga a könyv.\n",
			size: 64,
			want: true,
		},
		{
			name: "toc only",
			head: "Tartalom\n1. fejezet .... 5\n2. fejezet .... 9\n3. fejezet .... 12\n4. fejezet .... 20\n5. fejezet .... 30\n",
			size: 120,
			want: true,
		},
		{
			name: "normal title page",
			head: "Móricz Zsigmond\n\nAz arany ember\n\nA reggel még hűvös volt, a folyó felett köd ült.\n",
			size: 90,
			want: false,
		},
		{
			name: "short poem",
			head: "Anyámnak\n\nA fűzfa sír, a szél zokog,\nlevelek hullnak, mint a könnyek.\n",
			size: 70,
			want: false,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := isJunkContent(c.head, c.size); got != c.want {
				t.Errorf("isJunkContent(%q, %d) = %v, want %v", c.head, c.size, got, c.want)
			}
		})
	}
}
