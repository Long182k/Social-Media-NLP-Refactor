package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"time"

	"github.com/redis/go-redis/v9"

	"github.com/social-media-nlp/refactor/backend/eval"
	"github.com/social-media-nlp/refactor/backend/internal/nlp/worker"
)

func main() {
	cloud := flag.Bool("cloud", false, "evaluate both approved Workers Free models with the shared budget")
	initialize := flag.Bool("initialize-budget", false, "initialize today's shared ledger using freshly verified account usage")
	flag.Parse()
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt)
	defer cancel()
	if err := run(ctx, *cloud, *initialize); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run(ctx context.Context, cloud, initialize bool) error {
	if cloud && initialize {
		return errors.New("run budget initialization and evaluation separately")
	}
	examples, digest, err := eval.Fixture()
	if err != nil {
		return err
	}
	report := worker.Evaluation{FixtureSHA256: digest, Local: eval.LocalScores(examples)}
	out := json.NewEncoder(os.Stdout)
	out.SetIndent("", "  ")
	if !cloud && !initialize {
		return out.Encode(report)
	}

	evidence, err := worker.ReadEvidence(os.Getenv("NLP_CLOUD_EVIDENCE"))
	if err != nil {
		return err
	}
	options, err := redis.ParseURL(os.Getenv("VALKEY_URL"))
	if err != nil {
		return errors.New("a shared VALKEY_URL is required")
	}
	cache := redis.NewClient(options)
	defer cache.Close()
	if initialize {
		return worker.InitializeBudget(ctx, cache, evidence, time.Now())
	}
	client, err := worker.NewCloudClient(evidence, os.Getenv("NLP_CLOUD_TOKEN"), cache)
	if err != nil {
		return err
	}
	report.AccountID = evidence.AccountID
	for _, model := range []string{worker.PrimaryModel, worker.SecondaryModel} {
		result, err := client.EvaluateModel(ctx, model, examples)
		report.Models = append(report.Models, result)
		if err != nil {
			return errors.Join(err, out.Encode(report))
		}
	}
	return out.Encode(report)
}
