package main

import "strings"

// ModelCatalog maps supported model aliases to the defined GeneratorModels.
var ModelCatalog = map[string]string{
	// Full names
	"ox-alpha-free":            GeneratorModel1,
	"glm-5.3-flash":            GeneratorModel2,
	"mimo-v2.5":                GeneratorModel3,
	"gpt-5.6-luna":             GeneratorModel4,
	"qwen3.8-flash":            GeneratorModel5,
	"deepseek-v4-flash":        GeneratorModel6,
	"liquid/lfm-2.5-2.6b:free": GeneratorModel7,
	"z-ai/glm-5.2:free":        GeneratorModel8,

	// Provider-prefixed or alternate aliases
	"opencode-go/ox-alpha-free":     GeneratorModel1,
	"opencode-go/glm-5.3-flash":     GeneratorModel2,
	"opencode-go/mimo-v2.5":         GeneratorModel3,
	"oopenai/gpt-5.6-luna":          GeneratorModel4,
	"openai/gpt-5.6-luna":           GeneratorModel4,
	"opencode-go/qwen3.8-flash":     GeneratorModel5,
	"opencode-go/deepseek-v4-flash": GeneratorModel6,

	// Short names / keywords
	"ox-alpha":       GeneratorModel1,
	"ox":             GeneratorModel1,
	"glm":            GeneratorModel2,
	"mimo":           GeneratorModel3,
	"luna":           GeneratorModel4,
	"gpt":            GeneratorModel4,
	"qwen":           GeneratorModel5,
	"qwen3.8":        GeneratorModel5,
	"deepseek":       GeneratorModel6,
	"deepseek-flash": GeneratorModel6,
	"lfm-2.5":        GeneratorModel7,
	"lfm":            GeneratorModel7,
	"glm-5.2":        GeneratorModel8,

	// Legacy aliases
	"smollm": GeneratorModel7,
}

// ResolveModel returns the corresponding generator model for any model name or alias.
func ResolveModel(name string) string {
	trimmed := strings.TrimSpace(name)
	if trimmed == "" {
		return GeneratorModel
	}
	if resolved, ok := ModelCatalog[strings.ToLower(trimmed)]; ok {
		return resolved
	}
	for _, m := range []string{
		GeneratorModel1, GeneratorModel2, GeneratorModel3,
		GeneratorModel4, GeneratorModel5, GeneratorModel6,
		GeneratorModel7, GeneratorModel8,
	} {
		if strings.EqualFold(trimmed, m) {
			return m
		}
	}
	// Fallback to passing the exact string if given
	return trimmed
}
