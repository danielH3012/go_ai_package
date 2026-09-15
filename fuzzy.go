package goaipackage

import (
	"fmt"
	"regexp"
	"strings"
)

var normalizeStripRegexp = regexp.MustCompile(`[^a-z0-9\s]+`)

func normalizeString(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	s = normalizeStripRegexp.ReplaceAllString(s, " ")
	return strings.Join(strings.Fields(s), " ")
}

func levenshtein(a, b string) int {
	if a == "" {
		return len(b)
	}
	if b == "" {
		return len(a)
	}

	prev := make([]int, len(b)+1)
	curr := make([]int, len(b)+1)

	for j := 0; j <= len(b); j++ {
		prev[j] = j
	}

	for i := 1; i <= len(a); i++ {
		curr[0] = i
		for j := 1; j <= len(b); j++ {
			cost := 1
			if a[i-1] == b[j-1] {
				cost = 0
			}
			del := prev[j] + 1
			ins := curr[j-1] + 1
			sub := prev[j-1] + cost
			curr[j] = del
			if ins < curr[j] {
				curr[j] = ins
			}
			if sub < curr[j] {
				curr[j] = sub
			}
		}
		prev, curr = curr, prev
	}

	return prev[len(b)]
}

func similarityScore(a, b string) float64 {
	na := normalizeString(a)
	nb := normalizeString(b)
	if na == "" || nb == "" {
		return 0
	}
	if na == nb {
		return 1.0
	}

	if len(na) >= 3 {
		if strings.Contains(nb, na) || strings.Contains(na, nb) {
			longer := len(na)
			if len(nb) > longer {
				longer = len(nb)
			}
			return 0.75 + 0.25*float64(min(len(na), len(nb)))/float64(longer)
		}

		tokensA := strings.Fields(na)
		tokensB := strings.Fields(nb)
		if len(tokensA) > 0 && len(tokensB) > 0 {
			common := 0
			for _, ta := range tokensA {
				for _, tb := range tokensB {
					if ta == tb {
						common++
						break
					}
				}
			}
			if common > 0 {
				return float64(common) / float64(max(len(tokensA), len(tokensB)))
			}
		}
	}

	if len(na) < 4 || len(nb) < 4 {
		return 0
	}

	dist := levenshtein(na, nb)
	maxLen := max(len(na), len(nb))
	return 1.0 - float64(dist)/float64(maxLen)
}

type RecordMatch struct {
	Record     map[string]any
	Field      string
	CanonValue any
	IDField    string
	IDValue    any
	Score      float64
	Ambiguous  bool
}

func bestMatch(value string, records []map[string]any, idField string, threshold float64) *RecordMatch {
	if value == "" || len(records) == 0 {
		return nil
	}

	var best *RecordMatch
	var secondBestScore float64

	for _, r := range records {
		currID := fmt.Sprintf("%v", r[idField])
		for fieldName, fieldVal := range r {
			fieldStr, ok := fieldVal.(string)
			if !ok {
				continue
			}
			score := similarityScore(value, fieldStr)
			if score < threshold {
				continue
			}

			if best == nil || score > best.Score {
				if best != nil && currID != fmt.Sprintf("%v", best.IDValue) {
					secondBestScore = best.Score
				}
				best = &RecordMatch{
					Record:     r,
					Field:      fieldName,
					CanonValue: fieldVal,
					IDField:    idField,
					IDValue:    r[idField],
					Score:      score,
				}
			} else if currID != fmt.Sprintf("%v", best.IDValue) && score > secondBestScore {
				secondBestScore = score
			}
		}
	}

	// If there is another record with an almost identical score (diff < 0.08), flag as ambiguous
	if best != nil && secondBestScore > 0 && (best.Score-secondBestScore) < 0.08 {
		best.Ambiguous = true
	}

	return best
}
