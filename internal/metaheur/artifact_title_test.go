package metaheur

import "testing"

func TestIsArtifactTitle(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want bool
	}{
		{"copyright stem", ",COPYRIGHT.23081", true},
		{"title stem", ",TITLE.18901", true},
		{"framemaker stem", ",aspcTOC.fm.21592", true},
		{"chapter stem", ",ch01.29067", true},
		{"chapter copy stem", ",ch03copy.28361", true},
		{"leading comma short", ",r", true},
		{"absolute directory", "/ELP/", true},
		{"scribd path", "/opt/scribd/conversion/tmp/scratch2508/30747800.txt.utf8", true},
		{"windows path", `D:\ Munka\ Park\ viktor.qxd`, true},
		{"cert path", "CertPrs8/Java 5 Cert. Study Guide/Sierra-Bates/225360-6/Chapter 1", true},
		{"framemaker extension", "CH11.vp", true},
		{"book extension", "AAL2E_03.book", true},
		{"txt extension", "tutorfinal.txt", true},
		{"pdf extension", "Complete PDF1-4.pdf", true},
		{"underscore and extension", "Spring_Book.book", true},
		{"dotted file stem", "Prentice.Hall.Filthy.Rich.Clients.Aug.2007", true},
		{"dotted file stem with dashes", "Manning.-.Java.Persistence.with.Hibernate", true},
		{"underscore title", "A_mesterlovesz", true},
		{"converter prefix", "Microsoft Word - A V.2k.sz.rtf", true},
		{"converter prefix without extension", "Microsoft Word - Isaac Asimov - A robot korrektor", true},
		{"structural label", "Chapter 13", true},
		{"short structural label", "ch03", true},
		{"volume label", "Volume I", true},
		{"hungarian volume label", "I. Kötet", true},
		{"internal cover label", "Cover", true},
		{"internal glossary label", "Glossary", true},
		{"toc label", "Table of Contents", true},
		{"hungarian toc", "TARTALOMJEGYZÉK", true},
		{"broken encoding", "L\uFFFDSZOMJ", true},
		{"control character", "drupa\x006", true},
		{"plain title", "Dune", false},
		{"year title", "1984", false},
		{"dotted abbreviation", "U.S.A.", false},
		{"letter acronym", "S.T.A.L.K.E.R.", false},
		{"programming title", "C++ GUI Programming with Qt 4", false},
		{"fraction title", "A 23 és 3/4 éves Adrian Mole küzdelmei", false},
		{"slash abbreviation", "TCP/IP Illustrated", false},
		{"series issue", "Egy másik asszony (Júlia Bestseller 1995/3)", false},
		{"abbreviation title", "Dr. No", false},
		{"punctuated acronym", "M*A*S*H", false},
		{"long title", "The Go Programming Language", false},
		{"hungarian title", "A vízidisznók gyöngyökről álmodnak 2. 2006", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := IsArtifactTitle(c.in); got != c.want {
				t.Errorf("IsArtifactTitle(%q) = %v, want %v", c.in, got, c.want)
			}
		})
	}
}
