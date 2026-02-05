package textanalyzer

import (
	"regexp"
	"strings"
)

// Analyzer is the interface that all our text analyzers will implement.
type Analyzer interface {
	// Analyze takes a text string and transforms it into a slice of tokens.
	Analyze(text string) []string
}

// --- Reusable Components ---

// tokenizerRegex is a regular expression to extract words.
// [\p{L}0-9_]+ matches letters, numbers, and underscores.
var tokenizerRegex = regexp.MustCompile(`[\p{L}0-9_]+`)

// Tokenize splits text into a slice of lowercase words.
func Tokenize(text string) []string {
	text = strings.ToLower(text)
	return tokenizerRegex.FindAllString(text, -1)
}

// englishStopWords is a map of common English words to ignore.
var englishStopWords = map[string]struct{}{
	"a": {}, "an": {}, "and": {}, "are": {}, "as": {}, "at": {}, "be": {}, "by": {},
	"for": {}, "from": {}, "has": {}, "he": {}, "in": {}, "is": {}, "it": {}, "its": {},
	"of": {}, "on": {}, "that": {}, "the": {}, "to": {}, "was": {}, "were": {}, "will": {}, "with": {},
}

// FilterEnglishStopWords removes common words from a slice of tokens.
func FilterEnglishStopWords(tokens []string) []string {
	// Pre-allocate a slice with a reasonable capacity to avoid reallocations.
	filtered := make([]string, 0, len(tokens))
	for _, token := range tokens {
		if _, isStopWord := englishStopWords[token]; !isStopWord {
			filtered = append(filtered, token)
		}
	}
	return filtered
}

// --- Italian Stop Words ---

var italianStopWords = map[string]struct{}{
	"a": {}, "ad": {}, "al": {}, "allo": {}, "ai": {}, "agli": {}, "all": {}, "agl": {}, "alla": {}, "alle": {},
	"con": {}, "col": {}, "coi": {}, "da": {}, "dal": {}, "dallo": {}, "dai": {}, "dagli": {}, "dall": {}, "dagl": {}, "dalla": {}, "dalle": {},
	"di": {}, "del": {}, "dello": {}, "dei": {}, "degli": {}, "dell": {}, "degl": {}, "della": {}, "delle": {},
	"e": {}, "ed": {}, "in": {}, "nel": {}, "nello": {}, "nei": {}, "negli": {}, "nell": {}, "negl": {}, "nella": {}, "nelle": {},
	"su": {}, "sul": {}, "sullo": {}, "sui": {}, "sugli": {}, "sull": {}, "sugl": {}, "sulla": {}, "sulle": {},
	"per": {}, "tra": {}, "contro": {}, "io": {}, "tu": {}, "lui": {}, "lei": {}, "noi": {}, "voi": {}, "loro": {},
	"mio": {}, "mia": {}, "miei": {}, "mie": {}, "tuo": {}, "tua": {}, "tuoi": {}, "tue": {}, "suo": {}, "sua": {}, "suoi": {}, "sue": {},
	"nostro": {}, "nostra": {}, "nostri": {}, "nostre": {}, "vostro": {}, "vostra": {}, "vostri": {}, "vostre": {},
	"mi": {}, "ti": {}, "ci": {}, "vi": {}, "lo": {}, "la": {}, "li": {}, "le": {}, "gli": {}, "ne": {},
	"il": {}, "un": {}, "uno": {}, "una": {}, "ma": {}, "se": {}, "perché": {}, "anche": {}, "come": {},
	"dov": {}, "dove": {}, "che": {}, "chi": {}, "cui": {}, "non": {}, "più": {}, "quale": {}, "quanto": {}, "quanti": {},
	"quanta": {}, "quante": {}, "quello": {}, "quelli": {}, "quella": {}, "quelle": {}, "questo": {}, "questi": {},
	"questa": {}, "queste": {}, "si": {}, "ho": {}, "hai": {}, "ha": {}, "abbiamo": {}, "avete": {}, "hanno": {},
	"abbia": {}, "abbiate": {}, "abbiano": {}, "avrò": {}, "avrai": {}, "avrà": {}, "avremo": {}, "avrete": {}, "avranno": {},
	"avrei": {}, "avresti": {}, "avrebbe": {}, "avremmo": {}, "avreste": {}, "avrebbero": {}, "avevo": {}, "avevi": {},
	"aveva": {}, "avevamo": {}, "avevate": {}, "avevano": {}, "ebbi": {}, "avesti": {}, "ebbe": {}, "avemmo": {},
	"aveste": {}, "ebbero": {}, "fui": {}, "fosti": {}, "fu": {}, "fummo": {}, "foste": {}, "furono": {},
	"ero": {}, "eri": {}, "era": {}, "eravamo": {}, "eravate": {}, "erano": {}, "sarei": {}, "saresti": {},
	"sarebbe": {}, "saremmo": {}, "sareste": {}, "sarebbero": {}, "sono": {}, "sei": {}, "è": {}, "siamo": {},
	"siete": {}, "sia": {}, "siate": {}, "siano": {}, "sto": {}, "stai": {}, "sta": {}, "stiamo": {}, "state": {}, "stanno": {},
}

// FilterItalianStopWords removes common Italian words.
func FilterItalianStopWords(tokens []string) []string {
	filtered := make([]string, 0, len(tokens))
	for _, token := range tokens {
		if _, isStopWord := italianStopWords[token]; !isStopWord {
			filtered = append(filtered, token)
		}
	}
	return filtered
}
