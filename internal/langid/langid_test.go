package langid

import (
	"strings"
	"testing"
)

var samples = map[string]string{
	"hu": "A magyar nyelv az uráli nyelvcsalád tagja, és a honfoglalás óta " +
		"több mint ezer éve beszélik a Kárpát-medencében. A magyar irodalom " +
		"gazdag hagyományokkal rendelkezik, a népmeséktől a modern regényekig. " +
		"Ez a szöveg kizárólag annak érdekében íródott, hogy a nyelvfelismerő " +
		"működését ellenőrizni lehessen rajta.",
	"en": "The quick brown fox jumps over the lazy dog. Language detection " +
		"should identify this passage as English without any doubt, because it " +
		"contains many common English words and a familiar sentence structure. " +
		"This sample is long enough for a statistical detector to work with.",
	"de": "Die deutsche Sprache gehört zu den germanischen Sprachen und wird " +
		"von mehr als hundert Millionen Menschen gesprochen. Dieser Text dient " +
		"dazu, die Spracherkennung zu prüfen, und enthält deshalb viele " +
		"typische deutsche Wörter und Satzstrukturen.",
	"fr": "Le français est une langue romane parlée par des millions de " +
		"personnes dans le monde entier. Ce texte a été écrit uniquement pour " +
		"vérifier le fonctionnement de la détection de langue, avec des mots " +
		"courants et une grammaire typique.",
	"es": "El español es una lengua romance que se habla en muchos países. " +
		"Este texto sirve para comprobar el funcionamiento del detector de " +
		"idiomas y contiene palabras y estructuras gramaticales habituales.",
	"ru": "Русский язык относится к восточнославянской группе языков и " +
		"используется миллионами людей. Этот текст написан для проверки " +
		"определения языка и содержит типичные русские слова и выражения.",
	"el": "Η ελληνική γλώσσα είναι μία από τις αρχαιότερες γλώσσες του " +
		"κόσμου και μιλιέται ακόμη και σήμερα. Αυτό το κείμενο γράφτηκε για " +
		"να δοκιμαστεί η αναγνώριση γλώσσας.",
	"tr": "Türkçe, Ural-Altay dil ailesine ait olduğu düşünülen bir dildir " +
		"ve milyonlarca insan tarafından konuşulur. Bu metin, dil algılama " +
		"sistemini denemek için yazılmıştır.",
	"ar": "اللغة العربية هي واحدة من أكثر اللغات انتشارا في العالم، ويتحدث بها " +
		"ملايين الناس في الوطن العربي. هذا النص مكتوب فقط لاختبار عمل نظام " +
		"التعرف على اللغة.",
	"fa": "زبان فارسی یکی از زبان\u200cهای هندواروپایی است که در ایران و کشورهای " +
		"همسایه صحبت می\u200cشود. این متن فقط برای آزمایش سامانه تشخیص زبان " +
		"نوشته شده است.",
	"hi": "हिन्दी भारत की सबसे अधिक बोली जाने वाली भाषाओं में से एक है। " +
		"यह पाठ भाषा पहचान प्रणाली की जाँच करने के लिए लिखा गया है और " +
		"इसमें सामान्य हिन्दी शब्द हैं।",
	"te": "తెలుగు దక్షిణ భారతదేశంలో మాట్లాడే ఒక ప్రధాన భాష. ఈ పాఠం భాష " +
		"గుర్తింపు వ్యవస్థను పరీక్షించడానికి మాత్రమే రాయబడింది మరియు " +
		"సాధారణ తెలుగు పదాలను కలిగి ఉంది.",
	"zh": "中文是世界上使用人数最多的语言之一，主要在中国和世界各地的华人社区中使用。" +
		"这段文字只是为了测试语言识别系统而写的，包含常见的汉语词汇和句式。",
	"ja": "日本語は日本で広く使われている言語であり、多くの人々によって話されています。" +
		"この文章は言語検出システムの動作を確認するためだけに書かれました。",
	"ko": "한국어는 한국에서 사용되는 언어이며 많은 사람들이 사용합니다. " +
		"이 글은 언어 감지 시스템의 작동을 확인하기 위해서만 작성되었습니다.",
}

func TestDetect(t *testing.T) {
	for want, text := range samples {
		t.Run(want, func(t *testing.T) {
			got, ok := Detect(text)
			if !ok {
				t.Fatalf("Detect(%s) returned no language", want)
			}
			if got.Code != want {
				t.Fatalf("Detect(%s) = %q (confidence %.2f), want %q", want, got.Code, got.Confidence, want)
			}
			if got.Confidence <= 0 {
				t.Fatalf("confidence for %s = %f, want > 0", want, got.Confidence)
			}
		})
	}
}

func TestDetectTooShort(t *testing.T) {
	if _, ok := Detect("hello"); ok {
		t.Fatal("Detect accepted a too-short sample")
	}
	if _, ok := Detect(strings.Repeat(" ", 500)); ok {
		t.Fatal("Detect accepted whitespace-only input")
	}
}

func BenchmarkDetect(b *testing.B) {
	text := strings.Repeat(samples["hu"]+" ", 8)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		Detect(text)
	}
}

func TestDetectProseIgnoresCodeLines(t *testing.T) {
	text := "@page { margin-bottom: 5.000000pt; margin-top: 5.000000pt; }\n" +
		"@font-face { font-family: Helvetica }\n" +
		"@font-face { font-family: \"Times New Roman\"; src: url(font.ttf); }\n" +
		"body { font-family: Helvetica; font-size: 12pt; }\n" +
		strings.Repeat(samples["hu"]+"\n", 6)
	got, ok := DetectProse(text)
	if !ok || got.Code != "hu" {
		t.Fatalf("DetectProse() = %+v, ok=%v, want hu", got, ok)
	}
}

func TestProseSampleKeepsNaturalLines(t *testing.T) {
	text := "Title: The Great Novel\nAuthor: Jane Doe\n\nA line of prose.\n" +
		"@media screen { body { color: black; } }\n"
	got := proseSample(text)
	if strings.Contains(got, "@media") || strings.Contains(got, "color:") {
		t.Errorf("proseSample kept code lines: %q", got)
	}
	for _, want := range []string{"Title: The Great Novel", "A line of prose."} {
		if !strings.Contains(got, want) {
			t.Errorf("proseSample dropped %q: %q", want, got)
		}
	}
}

func TestDetectProseStripsHTML(t *testing.T) {
	text := `<html><head><style>body { font-family: Helvetica; }</style></head>` +
		`<body><p class="calibre1">` + strings.Repeat(samples["hu"]+" ", 6) + `</p></body></html>`
	got, ok := DetectProse(text)
	if !ok || got.Code != "hu" {
		t.Fatalf("DetectProse() = %+v, ok=%v, want hu", got, ok)
	}
}

func TestProseSampleKeepsLongLinesWithMarkers(t *testing.T) {
	// Minified HTML/MOBI text often arrives as one long line; a stray
	// semicolon or colon must not discard the whole line.
	line := strings.Repeat("This is a long prose line; it has a colon: and more text. ", 4)
	got := proseSample("<p>" + line + "</p>")
	if !strings.Contains(got, "long prose line") {
		t.Fatalf("proseSample dropped a long prose line: %q", got)
	}
}
