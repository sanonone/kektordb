package server

import (
	"github.com/sanonone/kektordb/pkg/cognitive"
	"github.com/sanonone/kektordb/pkg/llm"
)

// LoadCognitiveConfig delegates to the shared cognitive config loader.
func LoadCognitiveConfig(path string) (cognitive.Config, llm.Config, error) {
	return cognitive.LoadConfig(path)
}
