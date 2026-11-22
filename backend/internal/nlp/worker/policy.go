package worker

import (
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"slices"
	"time"

	"github.com/social-media-nlp/refactor/backend/eval"
)

const (
	PrimaryModel   = "@cf/google/gemma-4-26b-a4b-it"
	SecondaryModel = "@cf/zai-org/glm-4.7-flash"
	DailyLimit     = 9000
)

var ErrIneligible = errors.New("missing, expired, or invalid Workers AI $0 eligibility")

type Evidence struct {
	AccountID           string    `json:"accountId"`
	Plan                string    `json:"plan"`
	VerifiedAt          time.Time `json:"verifiedAt"`
	NoPaymentMethod     bool      `json:"noPaymentMethod"`
	NoCredits           bool      `json:"noCredits"`
	HardDailyLimit      int       `json:"hardDailyLimit"`
	SharedBudget        bool      `json:"sharedBudget"`
	ValkeyNoEviction    bool      `json:"valkeyNoEviction"`
	ValkeyPersistent    bool      `json:"valkeyPersistent"`
	ValkeyDurableWrites bool      `json:"valkeyDurableWrites"`
	Models              []string  `json:"models"`
	UsageObservedAt     time.Time `json:"usageObservedAt"`
	NeuronsAlreadyUsed  int       `json:"neuronsAlreadyUsed"`
}

func ReadEvidence(path string) (Evidence, error) {
	var evidence Evidence
	if err := readJSON(path, &evidence); err != nil {
		return evidence, err
	}
	if err := evidence.verify(time.Now()); err != nil {
		return evidence, err
	}
	return evidence, nil
}

func (e Evidence) verify(now time.Time) error {
	account, err := hex.DecodeString(e.AccountID)
	age := now.Sub(e.VerifiedAt)
	if err != nil || len(account) != 16 || age < 0 || age > 24*time.Hour ||
		e.Plan != "workers-free" || !e.NoPaymentMethod || !e.NoCredits ||
		e.HardDailyLimit != 10000 || !e.SharedBudget || !e.ValkeyNoEviction || !e.ValkeyPersistent || !e.ValkeyDurableWrites ||
		!slices.Contains(e.Models, PrimaryModel) || !slices.Contains(e.Models, SecondaryModel) {
		return ErrIneligible
	}
	return nil
}

type ModelEvaluation struct {
	Model         string      `json:"model"`
	Scores        eval.Scores `json:"scores"`
	InputTokens   int64       `json:"inputTokens"`
	OutputTokens  int64       `json:"outputTokens"`
	Neurons       float64     `json:"neurons"`
	ReservedTotal int64       `json:"reservedNeurons"`
}

type Evaluation struct {
	AccountID     string            `json:"accountId,omitempty"`
	FixtureSHA256 string            `json:"fixtureSha256"`
	Local         eval.Scores       `json:"local"`
	Models        []ModelEvaluation `json:"models,omitempty"`
}

func ReadEvaluation(path, accountID string) (Evaluation, error) {
	var report Evaluation
	if err := readJSON(path, &report); err != nil {
		return report, err
	}
	examples, digest, err := eval.Fixture()
	if err != nil {
		return report, err
	}
	baseline := eval.LocalScores(examples)
	if report.AccountID != accountID || report.FixtureSHA256 != digest ||
		report.Local.Samples != baseline.Samples || report.Local.MacroF1 != baseline.MacroF1 {
		return report, errors.New("evaluation does not match the current account, dataset and baseline")
	}
	for _, name := range []string{PrimaryModel, SecondaryModel} {
		if !report.eligible(name) {
			return report, errors.New("both evaluated models must improve local macro-F1 within the daily budget")
		}
	}
	return report, nil
}

func (report Evaluation) eligible(model string) bool {
	for _, score := range report.Models {
		if score.Model == model && report.Local.Samples >= 60 && score.Scores.Samples == report.Local.Samples &&
			score.Scores.MacroF1 > report.Local.MacroF1 && score.Scores.MacroF1 <= 1 &&
			score.InputTokens > 0 && score.InputTokens <= int64(report.Local.Samples)*250_000 &&
			score.OutputTokens > 0 && score.OutputTokens <= int64(report.Local.Samples)*maxOutputTokens &&
			score.Neurons > 0 && score.Neurons == rates[model].neurons(score.InputTokens, score.OutputTokens) &&
			score.ReservedTotal > 0 && score.Neurons <= float64(score.ReservedTotal) && score.ReservedTotal <= DailyLimit {
			return true
		}
	}
	return false
}

func readJSON(path string, dst any) error {
	if path == "" {
		return errors.New("evidence or evaluation file required")
	}
	data, err := os.ReadFile(path) // #nosec G304 -- Operators configure this evidence file path.
	if err != nil {
		return err
	}
	return json.Unmarshal(data, dst)
}
