package textanalyzer

import (
	"regexp"
	"strings"
)

// Analyzer è l'interfaccia che tutti i nostri analizzatori di testo implementeranno.
type Analyzer interface {
	// Analyze prende una stringa di testo e la trasforma in una slice di token.
	Analyze(text string) []string
}

// --- Componenti Riutilizzabili ---

// tokenizerRegex è un'espressione regolare per estrarre parole.
// \p{L}+ corrisponde a sequenze di lettere in qualsiasi lingua (meglio di \w+).
var tokenizerRegex = regexp.MustCompile(`\p{L}+`)

// Tokenize divide un testo in una slice di parole in minuscolo.
func Tokenize(text string) []string {
	text = strings.ToLower(text)
	return tokenizerRegex.FindAllString(text, -1)
}

// stopWords è una mappa di parole comuni inglesi da ignorare.
var englishStopWords = map[string]struct{}{
	"a": {}, "an": {}, "and": {}, "are": {}, "as": {}, "at": {}, "be": {}, "by": {},
	"for": {}, "from": {}, "has": {}, "he": {}, "in": {}, "is": {}, "it": {}, "its": {},
	"of": {}, "on": {}, "that": {}, "the": {}, "to": {}, "was": {}, "were": {}, "will": {}, "with": {},
}

// FilterStopWords rimuove le parole comuni da una slice di token.
func FilterEnglishStopWords(tokens []string) []string {
	// Pre-alloca una slice con una capacità ragionevole per evitare riallocazioni.
	filtered := make([]string, 0, len(tokens))
	for _, token := range tokens {
		if _, isStopWord := englishStopWords[token]; !isStopWord {
			filtered = append(filtered, token)
		}
	}
	return filtered
}

// --- Stop Words Italiane (NUOVA AGGIUNTA) ---

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

// FilterItalianStopWords rimuove le parole comuni italiane (NUOVA FUNZIONE)
func FilterItalianStopWords(tokens []string) []string {
	filtered := make([]string, 0, len(tokens))
	for _, token := range tokens {
		if _, isStopWord := italianStopWords[token]; !isStopWord {
			filtered = append(filtered, token)
		}
	}
	return filtered
}
