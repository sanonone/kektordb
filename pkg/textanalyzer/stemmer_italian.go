package textanalyzer

import (
	"strings"
	"unicode"
)

// ItalianStemmer is an analyzer that tokenizes, filters stop words
// and applies the Snowball stemming algorithm for Italian.
type ItalianStemmer struct{}

// NewItalianStemmer creates a new Italian analyzer.
func NewItalianStemmer() *ItalianStemmer {
	return &ItalianStemmer{}
}

// Analyze implements the Analyzer interface.
func (s *ItalianStemmer) Analyze(text string) []string {
	tokens := Tokenize(text)
	tokens = FilterItalianStopWords(tokens) // Use the correct filter for Italian stop words
	stemmedTokens := make([]string, len(tokens))
	for i, token := range tokens {
		stemmedTokens[i] = stemItalian(token)
	}
	return stemmedTokens
}

// --- Italian Stemming Algorithm (Correct Implementation) ---

// isItalianVowel defines Italian vowels (without accents).
func isItalianVowel(r rune) bool {
	switch r {
	case 'a', 'e', 'i', 'o', 'u':
		return true
	}
	return false
}

// getItalianRegions calculates the R1, R2 and RV regions, fundamental for the algorithm.
func getItalianRegions(runes []rune) (r1, r2, rv int) {
	r1 = len(runes)
	r2 = len(runes)
	rv = len(runes)

	if len(runes) == 0 {
		return
	}

	// Calcolo RV
	if len(runes) > 2 {
		if !isItalianVowel(runes[1]) {
			// If the second letter is a consonant, RV is the region after the next vowel
			for i := 2; i < len(runes); i++ {
				if isItalianVowel(runes[i]) {
					rv = i + 1
					break
				}
			}
		} else if isItalianVowel(runes[0]) && isItalianVowel(runes[1]) {
			// If the first two letters are vowels, RV is the region after the next consonant
			for i := 2; i < len(runes); i++ {
				if !isItalianVowel(runes[i]) {
					rv = i + 1
					break
				}
			}
		} else {
			// If C-V at the start, RV starts at position 3
			rv = 3
		}
	}

	// Calculate R1 and R2
	for i := 1; i < len(runes); i++ {
		if isItalianVowel(runes[i-1]) && !isItalianVowel(runes[i]) {
			r1 = i + 1
			break
		}
	}
	for i := r1; i < len(runes); i++ {
		if isItalianVowel(runes[i-1]) && !isItalianVowel(runes[i]) {
			r2 = i + 1
			break
		}
	}

	return
}

// stemItalian is the main orchestrator of the stemming algorithm.
func stemItalian(word string) string {
	if len(word) < 3 {
		return word
	}

	// 1. Pre-processing: accent normalization and handling of intervocalic 'i'/'u'.
	s := strings.ToLower(word)
	s = strings.ReplaceAll(s, "à", "a")
	s = strings.ReplaceAll(s, "è", "e")
	s = strings.ReplaceAll(s, "ì", "i")
	s = strings.ReplaceAll(s, "ò", "o")
	s = strings.ReplaceAll(s, "ù", "u")

	runes := []rune(s)
	for i := 1; i < len(runes)-1; i++ {
		if (runes[i] == 'i' || runes[i] == 'u') && isItalianVowel(runes[i-1]) && isItalianVowel(runes[i+1]) {
			runes[i] = unicode.ToUpper(runes[i]) // Mark them to temporarily ignore
		}
	}

	r1, r2, rv := getItalianRegions(runes)
	s = string(runes)

	// 2. Execute Steps in correct sequence
	s = step0_pronouns(s, rv)

	sBeforeStep1 := s
	s = step1_standard_suffixes(s, r1, r2, rv)

	// Step 2 is executed ONLY if Step 1 did not make any changes.
	if s == sBeforeStep1 {
		s = step2_verb_suffixes(s, rv)
	}

	s = step3_final_vowels(s, rv)

	// 3. Post-processing: restore 'i' and 'u' marked earlier.
	s = strings.ReplaceAll(s, "I", "i")
	s = strings.ReplaceAll(s, "U", "u")

	return s
}

// step0_pronouns handles the removal of clitic pronouns.
func step0_pronouns(s string, rv int) string {
	pronouns := []string{
		"gliela", "gliele", "glieli", "glielo", "gliene", "cela", "cele", "celi", "celo", "cene",
		"mela", "mele", "meli", "melo", "mene", "tela", "tele", "teli", "telo", "tene",
		"vela", "vele", "veli", "velo", "vene", "ci", "gli", "la", "le", "li", "lo",
		"mi", "ne", "si", "ti", "vi",
	}

	for _, p := range pronouns {
		if newS, ok := replaceSuffixIfInRegionIT(s, rv, p, ""); ok {
			// After removing the pronoun, check for 'ch' or 'gh' and normalize them to 'c'/'g'
			if strings.HasSuffix(newS, "cher") || strings.HasSuffix(newS, "gher") {
				return newS[:len(newS)-2]
			}
			return newS
		}
	}
	return s
}

// step1_standard_suffixes removes the most common nominal and adverbial suffixes.
func step1_standard_suffixes(s string, r1, r2, rv int) string {
	suffixes := []struct {
		suf    string
		repl   string
		region *int // Pointer to the region to use (r1, r2, rv)
	}{
		{"mente", "", &rv}, {"atrice", "", &r2}, {"atrici", "", &r2},
		{"anza", "", &r1}, {"anze", "", &r1}, {"ico", "", &r1}, {"ici", "", &r1},
		{"ica", "", &r1}, {"ice", "", &r1}, {"iche", "", &r1}, {"ichi", "", &r1},
		{"ismo", "", &r1}, {"ismi", "", &r1}, {"ista", "", &r1}, {"iste", "", &r1},
		{"isti", "", &r1}, {"istà", "", &r1}, {"istè", "", &r1}, {"istì", "", &r1},
		{"oso", "", &r1}, {"osi", "", &r1}, {"osa", "", &r1}, {"ose", "", &r1},
		{"ità", "", &r1}, {"logia", "log", &r1}, {"logie", "log", &r1},
		{"azione", "", &r2}, {"azioni", "", &r2}, {"atore", "", &r2},
		{"abilità", "", &r2}, {"ibili", "", &r2}, {"abile", "", &r2},
		{"ività", "", &rv}, {"ivo", "", &rv}, {"ivi", "", &rv}, {"iva", "", &rv}, {"ive", "", &rv},
	}

	for _, suf := range suffixes {
		if newS, ok := replaceSuffixIfInRegionIT(s, *suf.region, suf.suf, suf.repl); ok {
			return newS
		}
	}
	return s
}

// step2_verb_suffixes removes a wide range of verb endings.
func step2_verb_suffixes(s string, rv int) string {
	verbSuffixes := []string{
		"erebbero", "irebbero", "assero", "assimo", "eranno", "erebbe", "eremmo", "ereste", "eresti", "essero", "iranno", "irebbe", "iremmo", "ireste", "iresti",
		"arono", "avamo", "avano", "avate", "eremo", "erete", "erono", "evamo", "evano", "evate", "iremo", "irete", "irono", "ivamo", "ivano", "ivate",
		"ammo", "ando", "asse", "assi", "emmo", "endo", "erai", "erei", "Yamo", "iamo", "immo", "irai", "irei", "isca", "isce", "isci", "isco",
		"ano", "are", "ata", "ate", "ati", "ato", "ava", "avi", "avo", "erà", "ere", "erò", "ete", "eva", "evi", "evo", "irà", "ire", "irò", "ita",
		"ite", "iti", "ito", "iva", "ivi", "ivo", "ono", "uta", "ute", "uti", "uto", "ar", "ir",
	}
	for _, suf := range verbSuffixes {
		if newS, ok := replaceSuffixIfInRegionIT(s, rv, suf, ""); ok {
			return newS
		}
	}
	return s
}

// step3_final_vowels removes residual final vowels.
func step3_final_vowels(s string, rv int) string {
	// Remove the final vowel if present (a, e, i, o)
	if strings.HasSuffix(s, "a") || strings.HasSuffix(s, "e") || strings.HasSuffix(s, "i") || strings.HasSuffix(s, "o") {
		if newS, ok := replaceSuffixIfInRegionIT(s, rv, s[len(s)-1:], ""); ok {
			return newS
		}
	}
	// Handle final 'ch' and 'gh' to normalize them to 'c' and 'g'
	if strings.HasSuffix(s, "chi") || strings.HasSuffix(s, "ghi") {
		if newS, ok := replaceSuffixIfInRegionIT(s, rv, s[len(s)-1:], ""); ok {
			return newS[:len(newS)-1]
		}
	}
	return s
}

// replaceSuffixIfInRegionIT is a helper function to replace a suffix only if it is in the correct region.
func replaceSuffixIfInRegionIT(s string, region int, old, new string) (string, bool) {
	if strings.HasSuffix(s, old) {
		// The starting position of the suffix must be >= the start of the region
		if len(s)-len(old) >= region {
			return s[:len(s)-len(old)] + new, true
		}
	}
	return s, false
}
