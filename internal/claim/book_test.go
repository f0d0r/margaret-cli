package claim

import "testing"

func TestResolveBook4620(t *testing.T) {
	t.Run("real windows", func(t *testing.T) {
		res := ResolveBook(kaoszFiles(
			[]string{"A KÁOSZ HŐSEI", "fantasy antológia", "Cherubion könyvkiadó 2004"},
			[]string{"A KÁOSZ HŐSEI", "fantasy antológia", "Cherubion könyvkiadó 2004"},
		))
		assertTitle(t, res, "(2004) A káosz hősei", StatusCorroborated)
		assertAuthor(t, res, "John Caldwell", StatusCorroborated)
		title := findCandidate(t, res, FieldTitle, "(2004) A káosz hősei")
		if title.Reason != "content_attestation_self" {
			t.Fatalf("reason = %q", title.Reason)
		}
		if title.SupportingWitnesses != 2 || title.StrictSupportingClaims != 1 {
			t.Fatalf("support = %d/%d, want 2/1", title.SupportingWitnesses, title.StrictSupportingClaims)
		}
		trunc := findCandidate(t, res, FieldTitle, "A KÁOSZ")
		if trunc.Disposition != DispositionCompatibleTruncation || trunc.UseForMatching {
			t.Fatalf("A KÁOSZ disposition = %q, use=%v", trunc.Disposition, trunc.UseForMatching)
		}
		mt := findCandidate(t, res, FieldAuthor, "MT")
		if mt.Disposition != DispositionOverruled || mt.UseForMatching {
			t.Fatalf("MT disposition = %q, use=%v", mt.Disposition, mt.UseForMatching)
		}
		if res.GroupingSuspect {
			t.Fatalf("unexpected grouping suspect: %s", res.GroupingReason)
		}
	})

	t.Run("2004 only in publisher line", func(t *testing.T) {
		res := ResolveBook(kaoszFiles(
			[]string{"A KÁOSZ HŐSEI", "fantasy antológia", "Cherubion könyvkiadó 2004"},
			[]string{"A KÁOSZ HŐSEI", "fantasy antológia", "Cherubion könyvkiadó 2004"},
		))
		title := findCandidate(t, res, FieldTitle, "(2004) A káosz hősei")
		if title.AttestationLevel != AttestPartial {
			t.Fatalf("level = %q, want partial even though 2004 is on the publisher line", title.AttestationLevel)
		}
		if title.Reason != "content_attestation_self" {
			t.Fatalf("reason = %q", title.Reason)
		}
	})

	t.Run("authorless title page has no text claim", func(t *testing.T) {
		files := kaoszFiles(
			[]string{"A KÁOSZ HŐSEI", "fantasy antológia"},
			[]string{"A KÁOSZ HŐSEI", "fantasy antológia"},
		)
		for _, file := range files {
			for _, c := range file.Claims {
				if c.Source == SourceText {
					t.Fatalf("fixture must not contain a text claim: %+v", c)
				}
			}
		}
		res := ResolveBook(files)
		assertTitle(t, res, "(2004) A káosz hősei", StatusCorroborated)
	})

	t.Run("cross-file attestation", func(t *testing.T) {
		files := kaoszFiles(nil, []string{"A KÁOSZ HŐSEI", "fantasy antológia"})
		res := ResolveBook(files)
		title := findCandidate(t, res, FieldTitle, "(2004) A káosz hősei")
		if title.Reason != "content_attestation_cross" {
			t.Fatalf("reason = %q, want cross", title.Reason)
		}
		if len(title.FoundIn) != 1 || title.FoundIn[0] != 7309 {
			t.Fatalf("found_in = %v", title.FoundIn)
		}
	})
}

func TestResolveBook4519(t *testing.T) {
	res := ResolveBook([]BookFile{{
		ID: 5621, Hash: "prc-4519", Path: "prc pack/(1994) Pont egy ponty.prc", SourceGroup: "prc pack",
		Window: Window{Lines: []string{"PONT EGY PONTY!", "John Caldwell"}},
		Claims: []Claim{
			titleMeta("(1994) Pont egy ponty", "metadata"),
			titleFile("(1994) Pont egy ponty", "filename:stem"),
			authorMetaAttested("John Caldwell", "metadata"),
			{Field: FieldTitle, Value: "PONT EGY PONTY!", Source: SourceText, Set: "text:titlepage", Trust: TrustWeak, Witness: WitnessDerived},
			{Field: FieldAuthor, Value: "John Caldwell", Source: SourceText, Set: "text:titlepage", Trust: TrustWeak, Witness: WitnessDerived},
		},
	}})
	assertTitle(t, res, "(1994) Pont egy ponty", StatusCorroborated)
	title := findCandidate(t, res, FieldTitle, "(1994) Pont egy ponty")
	if title.AttestationLevel != AttestPartial || title.Reason != "content_attestation_self" {
		t.Fatalf("level/reason = %q/%q", title.AttestationLevel, title.Reason)
	}
	text := findCandidate(t, res, FieldTitle, "PONT EGY PONTY!")
	if text.Disposition != DispositionCompatibleTruncation || text.UseForMatching {
		t.Fatalf("text disposition = %q, use=%v", text.Disposition, text.UseForMatching)
	}
}

func TestResolveBook3495(t *testing.T) {
	res := ResolveBook([]BookFile{{
		ID: 3495, Hash: "pdf-3495", Path: "Scott Rogers - Level Up! The Guide to Great Video Game Design.pdf", SourceGroup: "Programming Books",
		Claims: []Claim{
			{
				Field: FieldTitle, Value: "Level Up! The Guide to Great Video Game Design",
				Source: SourceFilename, Set: "filename:natural", Trust: TrustWeak, Witness: WitnessSupporting,
				AttestationLevel: AttestSecondary, Attestation: 0.5, AttestationMethod: "secondary_only",
			},
			{
				Field: FieldTitle, Value: "Scott Rogers - Level Up! The Guide to Great Video Game Design",
				Source: SourceFilename, Set: "filename:stem", Trust: TrustWeak, Witness: WitnessSupporting,
				AttestationLevel: AttestSecondary, Attestation: 0.5, AttestationMethod: "secondary_only",
			},
			{
				Field: FieldAuthor, Value: "Scott Rogers",
				Source: SourceFilename, Set: "filename:natural", Trust: TrustWeak, Witness: WitnessSupporting,
				AttestationLevel: AttestSecondary, Attestation: 0.5, AttestationMethod: "secondary_only",
			},
		},
	}})
	assertTitle(t, res, "Level Up! The Guide to Great Video Game Design", StatusWeak)
	assertAuthor(t, res, "Scott Rogers", StatusWeak)
	stem := findCandidate(t, res, FieldTitle, "Scott Rogers - Level Up! The Guide to Great Video Game Design")
	if stem.Disposition != DispositionOverruled || stem.UseForMatching {
		t.Fatalf("stem disposition = %q, use=%v", stem.Disposition, stem.UseForMatching)
	}
}

func TestResolveBookDune2(t *testing.T) {
	res := ResolveBook([]BookFile{{
		ID: 1, Hash: "dune2", Path: "Dune 2.epub", SourceGroup: "shelf",
		Window: Window{Lines: []string{"Dune"}},
		Claims: []Claim{titleMeta("Dune 2", "metadata")},
	}})
	assertTitle(t, res, "Dune 2", StatusSingle)
	if res.TitleStatus == StatusCorroborated {
		t.Fatalf("Dune 2 must not be corroborated by a Dune title page")
	}
}

func TestResolveBookAuthorNoise(t *testing.T) {
	t.Run("Li Wu is not noise", func(t *testing.T) {
		res := ResolveBook([]BookFile{
			{
				ID: 1, Hash: "a", Path: "a.epub", SourceGroup: "a",
				Claims: []Claim{titleMeta("Book", "metadata"), authorMeta("Li Wu", "metadata")},
			},
			{
				ID: 2, Hash: "b", Path: "b.epub", SourceGroup: "b",
				Claims: []Claim{
					titleMeta("Book", "metadata"),
					{Field: FieldAuthor, Value: "John Smith", Source: SourceMetadata, Set: "metadata", Trust: TrustStrong, Witness: WitnessIndependent, AttestationLevel: AttestFull},
				},
			},
		})
		li := findCandidate(t, res, FieldAuthor, "Li Wu")
		if li.Disposition == DispositionOverruled {
			t.Fatalf("Li Wu must not be overruled as noise")
		}
	})
	t.Run("JK alone is kept", func(t *testing.T) {
		res := ResolveBook([]BookFile{{
			ID: 1, Hash: "a", Path: "a.epub", SourceGroup: "a",
			Claims: []Claim{titleMeta("Book", "metadata"), authorMeta("JK", "metadata")},
		}})
		assertAuthor(t, res, "JK", StatusSingle)
	})
	t.Run("JK against attested author is overruled", func(t *testing.T) {
		res := ResolveBook([]BookFile{
			{
				ID: 1, Hash: "a", Path: "a.epub", SourceGroup: "a",
				Claims: []Claim{titleMeta("Book", "metadata"), authorMeta("JK", "metadata")},
			},
			{
				ID: 2, Hash: "b", Path: "b.epub", SourceGroup: "b",
				Claims: []Claim{
					titleMeta("Book", "metadata"),
					{Field: FieldAuthor, Value: "John Smith", Source: SourceMetadata, Set: "metadata", Trust: TrustStrong, Witness: WitnessIndependent, AttestationLevel: AttestFull},
				},
			},
		})
		jk := findCandidate(t, res, FieldAuthor, "JK")
		if jk.Disposition != DispositionOverruled || jk.UseForMatching {
			t.Fatalf("JK disposition = %q, use=%v", jk.Disposition, jk.UseForMatching)
		}
	})
}

func TestResolveBookMultiAuthorList(t *testing.T) {
	names := []string{"Brian Herbert", "Kevin J. Anderson", "J. K.", "Herbert"}
	claims := []Claim{titleMeta("A Hipertér Vándorai", "metadata")}
	for i, name := range names {
		claims = append(claims, Claim{
			Field: FieldAuthor, Value: name, Source: SourceMetadata,
			Set: "metadata", Ordinal: i, Trust: TrustStrong, Witness: WitnessIndependent,
		})
	}
	res := ResolveBook([]BookFile{{
		ID: 5697, Hash: "multi", Path: "multi.epub", SourceGroup: "shelf",
		Claims: claims,
	}})
	if len(res.Authors) < 2 {
		t.Fatalf("expected an author list, got %v", res.Authors)
	}
	found := false
	for _, c := range res.Candidates {
		if c.Field == FieldAuthor && len(c.Authors) >= 2 && c.Disposition == DispositionSelected {
			found = true
		}
	}
	if !found {
		t.Fatalf("no selected author-list candidate: %+v", res.Candidates)
	}
}

func TestResolveBookTitlelineWithoutClaims(t *testing.T) {
	res := ResolveBook([]BookFile{{
		ID: 1, Hash: "authorless", Path: "03.rtf", SourceGroup: "archive.zip",
		Window: Window{Lines: []string{"A KÁOSZ HŐSEI", "fantasy antológia"}},
	}})
	assertTitle(t, res, "A KÁOSZ HŐSEI", StatusWeak)
}

func TestTitleSignalsAndAttestation(t *testing.T) {
	value := `"És amikor õ (Jézus) kiment a földre, jõve elébe a városból egy ember, kiben ördögök voltak sok idõtõl fogva`
	signals := TitleSignals(value)
	if signalCount(signals) < 2 {
		t.Fatalf("signals = %d, want at least two", signals)
	}
	if !IsSuspicious(signals, false) {
		t.Fatal("unattested long sentence must be suspicious")
	}
	if IsSuspicious(signals, true) {
		t.Fatal("attested title must not be suspicious")
	}
	longTitle := `Egy nagyon hosszú, alcímes, tudományos cím, amely valóban létezik, és több részből áll, de attested`
	if IsSuspicious(TitleSignals(longTitle), true) {
		t.Fatal("attested long title must not be suspicious")
	}
}

func TestResolveBookSuspiciousMetadata(t *testing.T) {
	long := `"És amikor õ (Jézus) kiment a földre, jõve elébe a városból egy ember, kiben ördögök voltak sok idõtõl fogva`
	res := ResolveBook([]BookFile{{
		ID: 7338, Hash: "7338", Path: "Blatty, William Peter - Az �rd�g�z�.zip!az_ordoguzo.rtf", SourceGroup: "archive",
		Window: Window{Lines: []string{"az ordoguzo"}},
		Claims: []Claim{
			{Field: FieldTitle, Value: long, Source: SourceMetadata, Set: "metadata", Trust: TrustStrong, Witness: WitnessIndependent, SuspiciousSignals: TitleSignals(long)},
			{Field: FieldTitle, Value: "az ordoguzo", Source: SourceFilename, Set: "filename:stem", Trust: TrustWeak, Witness: WitnessSupporting},
			{Field: FieldAuthor, Value: "Zerit", Source: SourceMetadata, Set: "metadata", Trust: TrustStrong, Witness: WitnessIndependent},
		},
	}})
	assertTitle(t, res, "az ordoguzo", StatusWeak)
	meta := findCandidate(t, res, FieldTitle, long)
	if meta.Disposition != DispositionOverruled || meta.UseForMatching {
		t.Fatalf("suspicious metadata disposition = %q, use=%v", meta.Disposition, meta.UseForMatching)
	}
}

func TestResolveBookFullNameOverMononym(t *testing.T) {
	res := ResolveBook([]BookFile{{
		ID: 7338, Hash: "7338", Path: "Blatty, William Peter - Az �rd�g�z�.zip!az_ordoguzo.rtf", SourceGroup: "archive",
		Window: Window{Lines: []string{"az ordoguzo"}},
		Claims: []Claim{
			{Field: FieldTitle, Value: "az ordoguzo", Source: SourceFilename, Set: "filename:stem", Trust: TrustWeak, Witness: WitnessSupporting},
			{Field: FieldAuthor, Value: "Zerit", Source: SourceMetadata, Set: "metadata", Trust: TrustStrong, Witness: WitnessIndependent},
			{Field: FieldAuthor, Value: "Blatty, William Peter", Source: SourceFilename, Set: "filename:archive", Trust: TrustWeak, Witness: WitnessSupporting},
		},
	}})
	assertAuthor(t, res, "Blatty, William Peter", StatusWeak)
	zerit := findCandidate(t, res, FieldAuthor, "Zerit")
	if !zerit.UseForAgreement {
		t.Fatal("mononym alternative must remain available for agreement")
	}
	if zerit.UseForMatching {
		t.Fatal("mononym alternative must not be used for matching")
	}
}

func TestResolveBook6190Repaired(t *testing.T) {
	junkMeta := titleMeta("Microsoft Word - displayXhtml2.doc", "metadata")
	junkMeta.Junk = true
	res := ResolveBook([]BookFile{{
		ID: 6190, Hash: "pdf-6190",
		Path:   "/home/attila/books/Regények/K/Krasznahorkai László/Krasznahorkai László - Sátántangó.pdf",
		Window: Window{FileID: 6190, Lines: []string{"++ 0"}},
		Claims: []Claim{
			titleFile("Sátántangó", "filename:natural"),
			titleFile("Krasznahorkai László - Sátántangó", "filename:stem"),
			junkMeta,
			{Field: FieldAuthor, Value: "Krasznahorkai László", Source: SourceFilename, Set: "filename:natural", Trust: TrustWeak, Witness: WitnessSupporting},
			authorMeta("mr", "metadata"),
		},
	}})
	assertTitle(t, res, "Sátántangó", StatusWeak)
	assertAuthor(t, res, "Krasznahorkai László", StatusWeak)
	mr := findCandidate(t, res, FieldAuthor, "mr")
	if mr.UseForMatching || !mr.UseForAgreement {
		t.Fatalf("mr = use_for_matching=%v use_for_agreement=%v", mr.UseForMatching, mr.UseForAgreement)
	}
	for _, c := range res.Candidates {
		if c.Field == FieldTitle && c.Value == "++ 0" && c.Disposition == DispositionSelected {
			t.Fatal("titleline must not win over the repaired filename title")
		}
	}
}

func TestResolveBookParentAuthorFallback(t *testing.T) {
	res := ResolveBook([]BookFile{{
		ID: 6190, Hash: "pdf-6190",
		Path: "/home/attila/books/Regények/K/Krasznahorkai László/Krasznahorkai Lászl - Satantango.pdf",
		Claims: []Claim{
			titleFile("Sátántangó", "filename:natural"),
			{Field: FieldAuthor, Value: "Krasznahorkai László", Source: SourceFilename, Set: "filename:path-author", Trust: TrustWeak, Witness: WitnessSupporting},
			authorMeta("mr", "metadata"),
		},
	}})
	assertAuthor(t, res, "Krasznahorkai László", StatusWeak)
	mr := findCandidate(t, res, FieldAuthor, "mr")
	if mr.UseForMatching || !mr.UseForAgreement {
		t.Fatalf("mr = use_for_matching=%v use_for_agreement=%v", mr.UseForMatching, mr.UseForAgreement)
	}
}

func TestResolveBookGroupingSuspect(t *testing.T) {
	res := ResolveBook([]BookFile{
		{
			ID: 1, Hash: "a", Path: "a.epub", SourceGroup: "a",
			Claims: []Claim{titleMeta("Pragmatic Flutter", "metadata"), authorMeta("Priyanka Tyagi", "metadata")},
		},
		{
			ID: 2, Hash: "b", Path: "b.epub", SourceGroup: "b",
			Claims: []Claim{titleMeta("A Mist of Prophecies", "metadata"), authorMeta("Steven Saylor", "metadata")},
		},
		{
			ID: 3, Hash: "c", Path: "c.epub", SourceGroup: "c",
			Claims: []Claim{titleMeta("Math and Art", "metadata"), authorMeta("Sasho Kalajdzievski", "metadata")},
		},
	})
	if !res.GroupingSuspect {
		t.Fatalf("expected grouping_suspect, title=%q", res.Title)
	}
}

func TestResolveBookGoldenlane(t *testing.T) {
	res := ResolveBook([]BookFile{{
		ID: 5761, Hash: "pdf-5761", Path: "Goldenlane Jane - Isteni balhé.pdf", SourceGroup: "Mega-Giga mini könyv pack-agika26/Goldenlane J",
		Claims: []Claim{
			titleMeta("Goldenlane Jane-Isteni balhé", "metadata"),
			{Field: FieldTitle, Value: "Isteni balhé", Source: SourceFilename, Set: "filename:natural", Trust: TrustWeak, Witness: WitnessSupporting},
			{Field: FieldTitle, Value: "Goldenlane Jane - Isteni balhé", Source: SourceFilename, Set: "filename:stem", Trust: TrustWeak, Witness: WitnessSupporting},
			authorMeta("BlueEyes", "metadata"),
			{Field: FieldAuthor, Value: "Goldenlane Jane", Source: SourceFilename, Set: "filename:natural", Trust: TrustWeak, Witness: WitnessSupporting},
		},
	}})
	assertTitle(t, res, "Goldenlane Jane-Isteni balhé", StatusSingle)
	assertAuthor(t, res, "BlueEyes", StatusSingle)
	if res.Authors[0] == "Goldenlane Jane" {
		t.Fatalf("title and author hypotheses must not be mixed")
	}
}

func kaoszFiles(prcWindow, rtfWindow []string) []BookFile {
	return []BookFile{
		{
			ID: 5620, Hash: "prc-4620", Path: "prc pack/(2004) A káosz hősei.prc", SourceGroup: "prc pack",
			Window: Window{FileID: 5620, Path: "prc pack/(2004) A káosz hősei.prc", Lines: prcWindow},
			Claims: []Claim{
				titleMeta("(2004) A káosz hősei", "metadata"),
				titleFile("(2004) A káosz hősei", "filename:stem"),
				authorMetaAttested("John Caldwell", "metadata"),
			},
		},
		{
			ID: 7309, Hash: "rtf-4620", Path: "A_extracted/A/kaosz.zip!kaosz_hosei.rtf", SourceGroup: "A_extracted/A/kaosz.zip",
			Window: Window{FileID: 7309, Path: "A_extracted/A/kaosz.zip!kaosz_hosei.rtf", Lines: rtfWindow},
			Claims: []Claim{
				titleMeta("A KÁOSZ", "metadata"),
				titleFile("kaosz hosei", "filename:stem"),
				authorMeta("MT", "metadata"),
			},
		},
		{
			// Exact duplicate path of the same content hash: must not add a
			// third supporting witness.
			ID: 7309, Hash: "rtf-4620", Path: "C/unpack/kaosz.zip!kaosz_hosei.rtf", SourceGroup: "C/unpack/kaosz.zip",
			Claims: []Claim{titleMeta("A KÁOSZ", "metadata")},
		},
	}
}

func titleMeta(value, set string) Claim {
	return Claim{Field: FieldTitle, Value: value, Source: SourceMetadata, Set: set, Trust: TrustStrong, Witness: WitnessIndependent}
}

func titleFile(value, set string) Claim {
	return Claim{Field: FieldTitle, Value: value, Source: SourceFilename, Set: set, Trust: TrustWeak, Witness: WitnessSupporting}
}

func authorMeta(value, set string) Claim {
	return Claim{Field: FieldAuthor, Value: value, Source: SourceMetadata, Set: set, Trust: TrustStrong, Witness: WitnessIndependent}
}

func authorMetaAttested(value, set string) Claim {
	c := authorMeta(value, set)
	c.AttestationLevel = AttestFull
	c.Attestation = 1
	c.AttestationMethod = "primary_full"
	return c
}

func assertTitle(t *testing.T, res BookResolution, value string, status Status) {
	t.Helper()
	if res.Title != value || res.TitleStatus != status {
		t.Fatalf("title = %q/%q, want %q/%q", res.Title, res.TitleStatus, value, status)
	}
}

func assertAuthor(t *testing.T, res BookResolution, value string, status Status) {
	t.Helper()
	if len(res.Authors) != 1 || res.Authors[0] != value || res.AuthorStatus != status {
		t.Fatalf("authors = %v/%q, want [%q]/%q", res.Authors, res.AuthorStatus, value, status)
	}
}

func findCandidate(t *testing.T, res BookResolution, field Field, value string) BookCandidate {
	t.Helper()
	for _, c := range res.Candidates {
		if c.Field == field && c.Value == value {
			return c
		}
	}
	t.Fatalf("candidate %s %q not found in %+v", field, value, res.Candidates)
	return BookCandidate{}
}
