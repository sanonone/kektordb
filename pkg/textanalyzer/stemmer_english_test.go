package textanalyzer

import "testing"

func TestStemPorter2(t *testing.T) {
	// Suite di test allineata con i risultati ufficiali dell'algoritmo Snowball (Porter2).
	testCases := []struct {
		input    string
		expected string
	}{
		// Casi Base
		{"", ""},
		{"a", "a"},
		{"run", "run"},
		// Step 0
		{"cat's", "cat"},
		{"cats'", "cat"},
		// Step 1a
		{"caresses", "caress"},
		{"ponies", "poni"},
		{"ties", "ti"},
		{"caress", "caress"},
		{"cats", "cat"},
		// Step 1b
		{"feed", "feed"},
		{"agreed", "agre"},
		{"plastered", "plaster"},
		{"motoring", "motor"},
		{"sing", "sing"},
		{"conflated", "conflat"},
		{"troubled", "troubl"},
		{"sized", "size"},
		{"hopping", "hop"},
		{"tanning", "tan"},
		{"falling", "fall"}, // Corretto
		{"hissing", "hiss"},
		{"fizzed", "fizz"},
		{"failing", "fail"},
		{"filing", "file"},
		// Step 1c
		{"happy", "happi"},
		{"sky", "ski"},
		// Step 2
		{"relational", "relat"},
		{"conditional", "condit"},
		{"rational", "ration"},
		{"valency", "valenc"},
		{"hesitancy", "hesit"},
		{"digitizer", "digit"},
		{"conformabli", "conform"},
		{"radicalli", "radic"},
		{"differentli", "differ"},
		{"vileli", "vile"},
		{"analogousli", "analog"},
		{"vietnamization", "vietnam"},
		{"predication", "predic"},
		{"operator", "oper"},
		{"feudalism", "feudal"},
		{"decisiveness", "decis"},
		{"hopefulness", "hope"},
		{"callousness", "callous"},
		{"formaliti", "formal"},
		{"sensitiviti", "sensit"},
		{"sensibiliti", "sensibl"},
		// Step 3
		{"triplicate", "triplic"},
		{"formative", "format"},
		{"formalize", "formal"},
		{"electriciti", "electr"},
		{"electrical", "electr"},
		{"hopeful", "hope"},
		{"goodness", "good"},
		// Step 4
		{"revival", "reviv"},
		{"allowance", "allow"},
		{"inference", "infer"},
		{"airliner", "airlin"},
		{"gyroscopic", "gyroscop"},
		{"adjustable", "adjust"},
		{"defensible", "defens"},
		{"irritant", "irrit"},
		{"replacement", "replac"},
		{"adjustment", "adjust"},
		// Step 5
		{"probate", "probat"},
		{"rate", "rate"},
		{"cease", "ceas"},
		{"controll", "control"},
		{"roll", "roll"},
	}

	for _, tc := range testCases {
		t.Run(tc.input, func(t *testing.T) {
			result := stemEnglish(tc.input)
			if result != tc.expected {
				t.Errorf("Stem('%s'): got '%s', want '%s'", tc.input, result, tc.expected)
			}
		})
	}
}
