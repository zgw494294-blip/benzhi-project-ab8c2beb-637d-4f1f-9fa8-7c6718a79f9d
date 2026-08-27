package archive

import (
	"sort"
	"strings"
)

func validDisclosure(value NameDisclosure) bool {
	return value == DisclosureForbidden || value == DisclosurePseudonym || value == DisclosureAllowed
}

func validCategory(value MarkCategory) bool {
	return value == CategoryIdentity || value == CategoryLocation || value == CategoryContact || value == CategoryTopic
}

func validStrategy(value RedactionStrategy) bool {
	return value == StrategyReplace || value == StrategyGeneralize || value == StrategyDelete
}

func nonblank(input []string) []string {
	result := make([]string, 0, len(input))
	for _, value := range input {
		if value = strings.TrimSpace(value); value != "" {
			result = append(result, value)
		}
	}
	return result
}

func unique(input []string) []string {
	seen := map[string]bool{}
	result := []string{}
	for _, value := range input {
		if !seen[value] {
			seen[value] = true
			result = append(result, value)
		}
	}
	sort.Strings(result)
	return result
}
