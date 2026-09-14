package goaipackage

import "strings"

// ModelCatalog maps supported model aliases to the defined GeneratorModels.
var ModelCatalog = map[string]string{
	"liquid/lfm-2.5-2.6b:free": GeneratorModel,
	"lfm-2.5":                  GeneratorModel,
	"lfm":                      GeneratorModel,
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
	// Fallback to passing the exact string if given
	return trimmed
}
