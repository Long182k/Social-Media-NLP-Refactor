package lexicon

import (
	"strings"
	"unicode"

	"golang.org/x/text/unicode/norm"
)

const (
	goodThreshold    = 0.5
	badThreshold     = -0.5
	negationFactor   = -0.85
	defaultMaxTokens = 64
)

// Score classifies text into GOOD, MODERATE, or BAD using a fast local VI/EN lexicon.
func Score(text string) Sentiment {
	s, _ := ScoreDetail(text)
	return s
}

// ScoreDetail returns both the 3-label Sentiment and the underlying numeric score.
func ScoreDetail(text string) (Sentiment, float64) {
	if strings.TrimSpace(text) == "" {
		return SentimentModerate, 0
	}

	// Fast path: avoid allocation when input is already NFC (true for almost all inputs).
	normalized := text
	if !norm.NFC.IsNormalString(text) {
		normalized = norm.NFC.String(text)
	}

	var score float64
	for _, r := range normalized {
		if w, ok := emojiWeights[r]; ok {
			score += w
		}
	}

	var stackBuf [defaultMaxTokens]string
	tokens := tokenizeInto(normalized, stackBuf[:0])
	n := len(tokens)
	if n == 0 {
		return classify(score), score
	}

	var negWindow int
	boost := 1.0

	for i := 0; i < n; {
		// 1. Check 3-grams
		if i+2 < n {
			tri := tokens[i] + " " + tokens[i+1] + " " + tokens[i+2]
			if w, ok := threeGrams[tri]; ok {
				score += applyModifiers(w, &negWindow, &boost)
				i += 3
				continue
			}
		}

		// 2. Check 2-grams (negators, intensifiers, sentiment phrases)
		if i+1 < n {
			bi := tokens[i] + " " + tokens[i+1]
			if _, isNeg := negators[bi]; isNeg {
				negWindow = 2
				i += 2
				continue
			}
			if mul, isInt := intensifiers[bi]; isInt {
				boost = mul
				if negWindow > 0 {
					negWindow--
				}
				i += 2
				continue
			}
			if w, ok := twoGrams[bi]; ok {
				score += applyModifiers(w, &negWindow, &boost)
				i += 2
				continue
			}
		}

		// 3. Check 1-gram
		tok := tokens[i]
		if _, isNeg := negators[tok]; isNeg || isNegationContraction(tok) {
			negWindow = 2
			i++
			continue
		}

		if tok == "vãi" || tok == "vai" {
			// Contextual slang booster if adjacent to a sentiment word; mild negative standalone.
			if (i+1 < n && hasSentimentWord(tokens[i+1])) || (i > 0 && hasSentimentWord(tokens[i-1])) {
				boost = 1.4
				if i > 0 && hasSentimentWord(tokens[i-1]) {
					// Retroactively amplify slightly if placed right after a sentiment word ("ngon vãi")
					if prevW, ok := unigrams[tokens[i-1]]; ok {
						score += prevW * 0.4
					}
					boost = 1.0
				}
			} else {
				score += applyModifiers(-0.4, &negWindow, &boost)
			}
			i++
			continue
		}

		if mul, isInt := intensifiers[tok]; isInt {
			boost = mul
			if negWindow > 0 {
				negWindow--
			}
			i++
			continue
		}

		if w, ok := unigrams[tok]; ok {
			score += applyModifiers(w, &negWindow, &boost)
		} else {
			if negWindow > 0 {
				negWindow--
			}
			boost = 1.0
		}
		i++
	}

	return classify(score), score
}

func classify(score float64) Sentiment {
	if score >= goodThreshold {
		return SentimentGood
	}
	if score <= badThreshold {
		return SentimentBad
	}
	return SentimentModerate
}

func applyModifiers(w float64, negWindow *int, boost *float64) float64 {
	val := w * (*boost)
	if *negWindow > 0 {
		val *= negationFactor
		*negWindow = 0
	}
	*boost = 1.0
	return val
}

func hasSentimentWord(tok string) bool {
	_, ok := unigrams[tok]
	return ok
}

func isNegationContraction(tok string) bool {
	return len(tok) > 3 && strings.HasSuffix(tok, "n't")
}

func tokenizeInto(s string, dst []string) []string {
	start := -1
	for i, r := range s {
		if isWordRune(r) {
			if start == -1 {
				start = i
			}
		} else if (r == '\'' || r == '’') && start != -1 {
			// Keep internal apostrophe for English contractions like don't / isn't
			continue
		} else if start != -1 {
			dst = append(dst, strings.ToLower(s[start:i]))
			start = -1
		}
	}
	if start != -1 {
		dst = append(dst, strings.ToLower(s[start:]))
	}
	return dst
}

func isWordRune(r rune) bool {
	return unicode.IsLetter(r) || unicode.IsDigit(r)
}
