package eval

import (
	"crypto/sha256"
	_ "embed"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strings"

	"github.com/social-media-nlp/refactor/backend/internal/nlp/lexicon"
)

//go:embed nlp_bilingual_benchmark.json
var fixtureJSON []byte

type Example struct {
	Language string            `json:"language"`
	Text     string            `json:"text"`
	Label    lexicon.Sentiment `json:"label"`
}

var labels = []lexicon.Sentiment{lexicon.SentimentGood, lexicon.SentimentModerate, lexicon.SentimentBad}

func Fixture() ([]Example, string, error) {
	var examples []Example
	if err := json.Unmarshal(fixtureJSON, &examples); err != nil {
		return nil, "", err
	}
	distribution := map[string]map[lexicon.Sentiment]int{"vi": {}, "en": {}}
	for _, sample := range examples {
		counts, known := distribution[sample.Language]
		if !known || !ValidLabel(sample.Label) || strings.TrimSpace(sample.Text) == "" {
			return nil, "", errors.New("invalid dataset example")
		}
		counts[sample.Label]++
	}
	target := len(examples) / 6
	if len(examples) < 60 || len(examples)%6 != 0 {
		return nil, "", errors.New("benchmark fixture requires at least 60 evenly balanced examples")
	}
	for _, counts := range distribution {
		for _, label := range labels {
			if counts[label] != target {
				return nil, "", errors.New("benchmark fixture must balance every language and label")
			}
		}
	}
	digest := sha256.Sum256(fixtureJSON)
	return examples, hex.EncodeToString(digest[:]), nil
}

func ValidLabel(label lexicon.Sentiment) bool {
	return label == lexicon.SentimentGood || label == lexicon.SentimentModerate || label == lexicon.SentimentBad
}

type Scores struct {
	MacroF1    float64                       `json:"macroF1"`
	PerLabelF1 map[lexicon.Sentiment]float64 `json:"perLabelF1"`
	Samples    int                           `json:"samples"`
}

func Measure(examples []Example, predicted []lexicon.Sentiment) Scores {
	scores := Scores{PerLabelF1: map[lexicon.Sentiment]float64{}, Samples: len(examples)}
	for _, label := range labels {
		var tp, fp, fn int
		for i, example := range examples {
			actual := example.Label == label
			guess := i < len(predicted) && predicted[i] == label
			switch {
			case actual && guess:
				tp++
			case guess:
				fp++
			case actual:
				fn++
			}
		}
		if denominator := 2*tp + fp + fn; denominator > 0 {
			scores.PerLabelF1[label] = float64(2*tp) / float64(denominator)
		} else {
			scores.PerLabelF1[label] = 0
		}
		scores.MacroF1 += scores.PerLabelF1[label] / float64(len(labels))
	}
	return scores
}

func LocalScores(examples []Example) Scores {
	predictions := make([]lexicon.Sentiment, 0, len(examples))
	for _, example := range examples {
		predictions = append(predictions, lexicon.Score(example.Text))
	}
	return Measure(examples, predictions)
}
