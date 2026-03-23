package cognitive

// SentimentLexicon holds positive and negative word roots for a language.
// Word roots are used with substring matching (strings.Contains) so a single
// root like "ottim" captures "ottimo", "ottima", "ottimi", "ottimizzare", etc.
type SentimentLexicon struct {
	Positive []string
	Negative []string
}

// sentimentLexicons maps supported text languages to their sentiment word roots.
var sentimentLexicons = map[string]SentimentLexicon{
	"english": {
		Positive: []string{"love", "great", "excellent", "good", "amazing", "prefer", "best"},
		Negative: []string{"hate", "terrible", "bad", "frustrating", "slow", "worst", "disappointing"},
	},
	"italian": {
		Positive: []string{"am", "ottim", "eccellent", "buon", "fantastic", "miglior", "piacevol", "perfett"},
		Negative: []string{"odi", "terribil", "brutt", "frustrant", "lent", "peggior", "deludent", "orribil"},
	},
}
