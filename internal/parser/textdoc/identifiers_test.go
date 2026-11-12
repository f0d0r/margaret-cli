package textdoc

import "testing"

func TestTextIdentifiersStandaloneOnly(t *testing.T) {
	cases := []struct {
		name string
		head string
		want string
	}{
		{
			name: "packt copyright page",
			head: "Published by Packt Publishing Ltd.\n32 Lincoln Road\nOlton\nBirmingham, B27 6PA, UK.\nISBN 978-1-847192-97-4\nwww.packtpub.com\n",
			want: "9781847192974",
		},
		{
			name: "wiley isbn 13 wins",
			head: "Published by\nWiley Publishing, Inc.\nISBN-13: 978-0-470-04641-8\nISBN-10: 0-470-04641-4\nManufactured in the United States of America\n",
			want: "9780470046418",
		},
		{
			name: "citation is not an identifier",
			head: "This is Vincent's third book; he is a co-author of\nJUnit in Action, published by Manning in 2003 (ISBN 1-930-11099-5) and Maven: A Developer's\nNotebook, published by O'Reilly in 2005 (ISBN 0-596-00750-7).\n",
			want: "",
		},
		{
			name: "isbn in prose is rejected",
			head: "The ISBN is 978-1-847192-97-4 according to the publisher.\n",
			want: "",
		},
		{
			name: "bare number is rejected",
			head: "978-1-847192-97-4\n",
			want: "",
		},
		{
			name: "two identifiers on one line are rejected",
			head: "ISBN 978-1-847192-97-4 ISBN 978-0-470-04641-8\n",
			want: "",
		},
		{
			name: "binding qualifier is accepted",
			head: "ISBN-13 (pbk): 978-1-847192-97-4\n",
			want: "9781847192974",
		},
		{
			name: "trailing binding qualifier is accepted",
			head: "ISBN 978-1-847192-97-4 (pbk.)\n",
			want: "9781847192974",
		},
		{
			name: "ebook label is accepted",
			head: "eISBN 978-1-847192-97-4\n",
			want: "9781847192974",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			ids := TextIdentifiers(c.head)
			got := ""
			if len(ids) > 0 {
				got = ids[0].Value
			}
			if got != c.want {
				t.Fatalf("TextIdentifiers = %q, want %q", got, c.want)
			}
		})
	}
}

// TestMavenTitlePageCitationHasNoISBN pins the real regression: the title
// page provides the correct title and authors, while the author bio cites
// JUnit in Action's ISBN in prose and must not become a text identifier.
func TestMavenTitlePageCitationHasNoISBN(t *testing.T) {
	head := "Better Builds with Maven\n" +
		"The How-to Guide for Maven 2.0\n" +
		"John Casey\nVincent Massol\nBrett Porter\nCarlos Sanchez\nJason van Zyl\n" +
		"Better Builds with Maven. The How-to Guide for Maven 2.0\n" +
		"© 2007 DevZuz\n" +
		"Vincent Massol: Vincent is a co-author of\n" +
		"JUnit in Action, published by Manning in 2003 (ISBN 1-930-11099-5) and Maven: A Developer's\n" +
		"Notebook, published by O'Reilly in 2005 (ISBN 0-596-00750-7).\n"
	if ids := TextIdentifiers(head); len(ids) != 0 {
		t.Fatalf("citation ISBN must not be a text identifier: %v", ids)
	}
	md := ExtractMetadata(head)
	if md.Title != "Better Builds with Maven: The How-to Guide for Maven 2.0" {
		t.Fatalf("title = %q", md.Title)
	}
	if len(md.Authors) != 5 {
		t.Fatalf("authors = %v", md.Authors)
	}
}

func TestContainsISBN(t *testing.T) {
	head := "JUnit in Action, published by Manning in 2003 (ISBN 1-930-11099-5).\n"
	if !ContainsISBN(head, "9781930110991") {
		t.Fatal("citation ISBN must still be detectable for fallback rejection")
	}
	if ContainsISBN(head, "9781847192974") {
		t.Fatal("unrelated ISBN must not be reported as present")
	}
}
