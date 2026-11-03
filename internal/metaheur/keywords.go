package metaheur

import "regexp"

// This file holds additive keyword hints used by the title-page heuristics.
// They are data, not logic: no feature is gated on a particular language being
// present, and a missing word only lowers recall for that language. Structural
// and script-based checks live in metaheur.go.

var (
	chapterHeading = regexp.MustCompile(`(?i)^(chapter|fejezet|prolog|epilog|eloszo|utoszo|tartalom|tartalomjegyzek|impresszum|ajanlas|motto|fuggelek|jegyzetek)\b`)
)

var chapterWords = map[string]struct{}{
	"chapter": {}, "fejezet": {},
	"prolog": {}, "prologus": {}, "epilog": {}, "epilogus": {},
	"eloszo": {}, "utoszo": {},
	"tartalom": {}, "tartalomjegyzek": {}, "impresszum": {},
	"ajanlas": {}, "motto": {}, "fuggelek": {}, "jegyzetek": {},
}

var articles = map[string]struct{}{
	"a": {}, "az": {}, "egy": {},
	"the": {}, "an": {},
	"der": {}, "die": {}, "das": {}, "ein": {}, "eine": {},
	"le": {}, "la": {}, "les": {}, "un": {}, "une": {},
	"el": {}, "los": {}, "las": {}, "una": {},
	"il": {}, "lo": {}, "gli": {}, "i": {},
}

var particles = map[string]struct{}{
	"de": {}, "van": {}, "von": {}, "di": {}, "du": {}, "le": {}, "la": {},
	"bin": {}, "ibn": {}, "del": {}, "della": {}, "dos": {}, "das": {},
	"der": {}, "den": {}, "ter": {}, "ten": {}, "al": {},
}

var creditStems = []string{
	"borito", "fedel", "fordit", "szerkeszt", "nyomd", "digitaliz", "szkenn",
	"korrekt", "impresszum", "tartalomjegyzek", "fulszoveg",
}

var creditWords = map[string]struct{}{
	"copyright": {},
	"ocr":       {},
	"mek":       {},
}

var creditPhrases = []string{
	"cover design", "cover art", "translated by", "edited by", "printed by",
	"all rights reserved", "minden jog fenntartva", "magyar elektronikus konyvtar",
}

var publisherStems = []string{"kiad", "konyvkiad"}

// organizationStems are folded substrings of institutions, libraries and
// companies. They can open a title page but never name a person, so ScoreName
// rejects them even where they appear in a capitalized, multi-word line.
var organizationStems = []string{
	"konyvtar", "akademia", "egyetem", "intezet", "alapitvany", "tarsasag",
	"muzeum", "miniszterium", "konyvkiad", "szerkesztoseg", "hivatal",
	"university", "institute", "academy", "foundation", "society",
	"association", "corporation", "publishing", "publishers", "library",
}

var publisherWords = map[string]struct{}{
	"publisher": {},
	"press":     {},
	"books":     {},
}

var publisherPhrases = []string{"published by"}
