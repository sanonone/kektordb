package textanalyzer

import "strings"

// EnglishStemmer is an analyzer that tokenizes, filters stop words
// and applies the Porter2 stemming algorithm for English.
type EnglishStemmer struct{}

// NewEnglishStemmer creates a new English analyzer.
func NewEnglishStemmer() *EnglishStemmer {
	return &EnglishStemmer{}
}

// Analyze implements the Analyzer interface.
func (s *EnglishStemmer) Analyze(text string) []string {
	tokens := Tokenize(text)
	tokens = FilterEnglishStopWords(tokens)
	stemmedTokens := make([]string, len(tokens))
	for i, token := range tokens {
		stemmedTokens[i] = stemEnglish(token)
	}
	return stemmedTokens
}

// --- Generic Support Functions (Used by both stemmers) ---

func replaceSuffixIfInRegion(s string, regionStart int, old, new string) (string, bool) {
	if strings.HasSuffix(s, old) {
		if len(s)-len(old) >= regionStart {
			return s[:len(s)-len(old)] + new, true
		}
	}
	return s, false
}

// --- English Stemming Algorithm (Porter2) ---

func isEnglishVowel(runes []rune, i int) bool {
	// --- SAFETY CHECK ---
	// We add a bounds guard to prevent any panic.
	if i < 0 || i >= len(runes) {
		return false
	}

	r := runes[i]
	switch r {
	case 'a', 'e', 'i', 'o', 'u':
		return true
	case 'y':
		// The 'y' is a vowel if it's not the first character and
		// the preceding character is NOT a vowel.
		if i == 0 {
			return false // 'y' at the start of a word is a consonant.
		}

		// Check the preceding character without recursion.
		prevRune := runes[i-1]
		switch prevRune {
		case 'a', 'e', 'i', 'o', 'u':
			return false // The preceding was a vowel, so 'y' is a consonant.
		default:
			// If the preceding is not a,e,i,o,u, then 'y' is a vowel.
			// (This doesn't handle the 'yy' case, but it's fine for now).
			return true
		}
	}
	return false
}

func getEnglishRegions(runes []rune) (r1, r2 int) {
	r1 = len(runes)
	r2 = len(runes)
	for i := 1; i < len(runes); i++ {
		if !isEnglishVowel(runes, i) && isEnglishVowel(runes, i-1) {
			r1 = i + 1
			break
		}
	}
	for i := r1 + 1; i < len(runes); i++ {
		if !isEnglishVowel(runes, i) && isEnglishVowel(runes, i-1) {
			r2 = i + 1
			break
		}
	}
	return
}

func endsWithShortSyllable(s string) bool {
	runes := []rune(s)
	l := len(runes)
	if l < 2 {
		return false
	}
	if l >= 3 && !isEnglishVowel(runes, l-3) && isEnglishVowel(runes, l-2) && !isEnglishVowel(runes, l-1) {
		last := runes[l-1]
		if last != 'w' && last != 'x' && last != 'y' {
			return true
		}
	}
	if l == 2 && isEnglishVowel(runes, 0) && !isEnglishVowel(runes, 1) {
		return true
	}
	return false
}

func stemEnglish(word string) string {
	if len(word) <= 2 {
		return word
	}
	exceptions1 := map[string]string{
		"skis": "ski", "skies": "sky", "dying": "die", "lying": "lie", "tying": "tie",
		"idly": "idl", "gently": "gentl", "ugly": "ugli", "early": "earli",
		"only": "onli", "singly": "singl", "news": "news", "howe": "howe",
		"atlas": "atlas", "cosmos": "cosmos", "bias": "bias", "andes": "andes",
	}
	if stem, ok := exceptions1[word]; ok {
		return stem
	}
	s := word
	if s[0] == '\'' {
		s = s[1:]
	}
	runes := []rune(s)
	if runes[0] == 'y' {
		runes[0] = 'Y'
	}
	s = string(runes)
	r1, r2 := getEnglishRegions(runes)

	s = englishStep0(s)
	s = englishStep1a(s)

	exceptions2 := []string{"inning", "outing", "canning", "herring", "earring", "proceed", "exceed", "succeed"}
	for _, e := range exceptions2 {
		if s == e {
			return s
		}
	}

	s = englishStep1b(s, r1)
	s = englishStep1c(s)
	s = englishStep2(s, r1)
	s = englishStep3(s, r1, r2)
	s = englishStep4(s, r2)
	s = englishStep5(s, r1)

	return strings.ToLower(s)
}

func englishStep0(s string) string {
	if strings.HasSuffix(s, "'s'") {
		return s[:len(s)-3]
	}
	if strings.HasSuffix(s, "'s") {
		return s[:len(s)-2]
	}
	if strings.HasSuffix(s, "'") {
		return s[:len(s)-1]
	}
	return s
}

func englishStep1a(s string) string {
	if strings.HasSuffix(s, "sses") {
		return s[:len(s)-2]
	}
	if strings.HasSuffix(s, "ies") {
		return s[:len(s)-2]
	}
	if strings.HasSuffix(s, "s") && !strings.HasSuffix(s, "ss") {
		if len(s) > 2 {
			runes := []rune(s[:len(s)-1])
			for i := range runes {
				if isEnglishVowel(runes, i) {
					return s[:len(s)-1]
				}
			}
		}
	}
	return s
}

func englishStep1b(s string, r1 int) string {
	if strings.HasSuffix(s, "eed") || strings.HasSuffix(s, "eedly") {
		if res, ok := replaceSuffixIfInRegion(s, r1, "eed", "ee"); ok {
			return res
		}
		if res, ok := replaceSuffixIfInRegion(s, r1, "eedly", "ee"); ok {
			return res
		}
		return s
	}
	stem := ""
	removed := false
	if strings.HasSuffix(s, "ed") || strings.HasSuffix(s, "edly") {
		stem = s[:len(s)-2]
		if strings.HasSuffix(s, "edly") {
			stem = s[:len(s)-4]
		}
		removed = true
	} else if strings.HasSuffix(s, "ing") || strings.HasSuffix(s, "ingly") {
		stem = s[:len(s)-3]
		if strings.HasSuffix(s, "ingly") {
			stem = s[:len(s)-5]
		}
		removed = true
	}
	if removed {
		runes := []rune(stem)
		hasVowel := false
		for i := range runes {
			if isEnglishVowel(runes, i) {
				hasVowel = true
				break
			}
		}
		if hasVowel {
			s = stem
			if strings.HasSuffix(s, "at") || strings.HasSuffix(s, "bl") || strings.HasSuffix(s, "iz") {
				s += "e"
			} else {
				l := len(s)
				if l > 1 && s[l-1] == s[l-2] {
					last := s[l-1]
					if last != 'l' && last != 's' && last != 'z' {
						s = s[:l-1]
					}
				} else {
					runes := []rune(s)
					r1_stem, _ := getEnglishRegions(runes)
					if endsWithShortSyllable(s) && r1_stem == len(s) {
						s += "e"
					}
				}
			}
		}
	}
	return s
}

func englishStep1c(s string) string {
	runes := []rune(s)
	l := len(runes)
	if l > 2 && (runes[l-1] == 'y' || runes[l-1] == 'Y') {
		if !isEnglishVowel(runes, l-2) {
			runes[l-1] = 'i'
			return string(runes)
		}
	}
	return s
}

func englishStep2(s string, r1 int) string {
	suffixes := []struct{ s1, s2 string }{
		{"ational", "ate"}, {"tional", "tion"}, {"enci", "ence"}, {"anci", "ance"},
		{"izer", "ize"}, {"abli", "able"}, {"alli", "al"}, {"entli", "ent"},
		{"eli", "e"}, {"ousli", "ous"}, {"ization", "ize"}, {"ation", "ate"},
		{"ator", "ate"}, {"alism", "al"}, {"iveness", "ive"}, {"fulness", "ful"},
		{"ousness", "ous"}, {"aliti", "al"}, {"iviti", "ive"}, {"biliti", "ble"},
		{"logi", "log"},
	}
	for _, suf := range suffixes {
		if newS, ok := replaceSuffixIfInRegion(s, r1, suf.s1, suf.s2); ok {
			return newS
		}
	}
	return s
}

func englishStep3(s string, r1, r2 int) string {
	suffixes := []struct{ s1, s2 string }{
		{"icate", "ic"}, {"ative", ""}, {"alize", "al"}, {"iciti", "ic"},
		{"ical", "ic"}, {"ful", ""}, {"ness", ""},
	}
	for _, suf := range suffixes {
		region := r1
		if suf.s1 == "ative" {
			region = r2
		}
		if newS, ok := replaceSuffixIfInRegion(s, region, suf.s1, suf.s2); ok {
			return newS
		}
	}
	return s
}

func englishStep4(s string, r2 int) string {
	suffixes := []string{
		"al", "ance", "ence", "er", "ic", "able", "ible", "ant", "ement",
		"ment", "ent", "ism", "ate", "iti", "ous", "ive", "ize",
	}
	if strings.HasSuffix(s, "ion") {
		if len(s)-3 >= r2 {
			stem := s[:len(s)-3]
			if strings.HasSuffix(stem, "s") || strings.HasSuffix(stem, "t") {
				return stem
			}
		}
	}
	for _, suf := range suffixes {
		if newS, ok := replaceSuffixIfInRegion(s, r2, suf, ""); ok {
			return newS
		}
	}
	return s
}

func englishStep5(s string, r1 int) string {
	if strings.HasSuffix(s, "e") {
		stem := s[:len(s)-1]
		if len(stem) >= r1 {
			runes := []rune(stem)
			r1_stem, _ := getEnglishRegions(runes)
			if !endsWithShortSyllable(stem) || r1_stem != len(stem) {
				s = stem
			}
		}
	}
	if strings.HasSuffix(s, "ll") {
		if len(s)-2 >= r1 {
			s = s[:len(s)-1]
		}
	}
	return s
}
