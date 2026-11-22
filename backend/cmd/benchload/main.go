package main

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
	"github.com/social-media-nlp/refactor/backend/internal/nlp/lexicon"
)

const (
	defaultVISample = "Hôm nay ứng dụng chạy cực kỳ mượt mà và giao diện xịn xò quá trời, nhưng hôm qua hơi lag nhẹ chút thôi ❤️"
	defaultENSample = "The new Go backend is super fast, really clean, and not buggy at all! Love this awesome upgrade 🔥"
)

type coldStartResult struct {
	TrialsMs []float64 `json:"trials_ms"`
	MinMs    float64   `json:"min_ms"`
	MedianMs float64   `json:"median_ms"`
	MaxMs    float64   `json:"max_ms"`
}

type feedResult struct {
	BaseURL       string  `json:"base_url"`
	Concurrency   int     `json:"concurrency"`
	Warmup        int     `json:"warmup"`
	Requests      int     `json:"requests"`
	OKCount       int64   `json:"ok_count"`
	ErrCount      int64   `json:"err_count"`
	ExpectedCount int     `json:"expected_count"`
	TotalMs       float64 `json:"total_ms"`
	RPS           float64 `json:"rps"`
	MinMs         float64 `json:"min_ms"`
	MeanMs        float64 `json:"mean_ms"`
	P50Ms         float64 `json:"p50_ms"`
	P95Ms         float64 `json:"p95_ms"`
	P99Ms         float64 `json:"p99_ms"`
	MaxMs         float64 `json:"max_ms"`
}

type nlpSampleStats struct {
	Warmup int     `json:"warmup"`
	Iters  int     `json:"iters"`
	MeanMs float64 `json:"mean_ms"`
	P50Ms  float64 `json:"p50_ms"`
	P95Ms  float64 `json:"p95_ms"`
	MinMs  float64 `json:"min_ms"`
	MaxMs  float64 `json:"max_ms"`
	Label  string  `json:"label"`
}

type nlpResult struct {
	InitMs float64        `json:"init_ms"`
	VI     nlpSampleStats `json:"vi"`
	EN     nlpSampleStats `json:"en"`
}

func main() {
	mode := flag.String("mode", "feed", "Mode: coldstart | wait-ready | feed | alice-uuid | nlp")
	probeURL := flag.String("url", "", "Readiness probe URL for coldstart/wait-ready")
	probeMode := flag.String("probe-mode", "plain", "Probe validation mode: plain | v2-readyz | v2-deps | legacy-profile | legacy-deps")
	expectID := flag.String("expect-id", "", "Expected user UUID for legacy-profile/legacy-deps/v2-deps probe")
	valkeyURL := flag.String("valkey-url", "", "Valkey URL for legacy-deps/v2-deps readiness verification")
	workdir := flag.String("workdir", ".", "Working directory for coldstart process")
	timeout := flag.Duration("timeout", 60*time.Second, "Readiness timeout")
	trials := flag.Int("trials", 5, "Number of cold-start trials")

	baseURL := flag.String("base-url", "http://127.0.0.1:18080", "API base URL for feed mode")
	loginUser := flag.String("login-user", "alice@example.com", "Login username/email")
	loginPass := flag.String("login-pass", "password", "Login password")
	warmup := flag.Int("warmup", 50, "Warmup request count")
	requests := flag.Int("requests", 500, "Measured request count")
	concurrency := flag.Int("concurrency", 10, "Concurrent worker count")
	limit := flag.Int("limit", 10, "Feed limit query parameter")
	expectedCount := flag.Int("expected-count", 10, "Expected number of items in data[]")

	nlpWarmup := flag.Int("nlp-warmup", 30, "NLP warmup iterations")
	nlpIters := flag.Int("nlp-iters", 500, "NLP measured iterations")

	flag.Parse()

	switch *mode {
	case "alice-uuid":
		fmt.Println(uuid.NewSHA1(uuid.NameSpaceURL, []byte("smn-seed-user:alice@example.com")).String())
	case "wait-ready":
		if *probeURL == "" {
			fatalf("-url is required for wait-ready")
		}
		if err := waitReady(*probeURL, *probeMode, *expectID, *valkeyURL, *loginUser, *loginPass, *timeout); err != nil {
			fatalf("wait-ready failed: %v", err)
		}
	case "coldstart":
		if *probeURL == "" {
			fatalf("-url is required for coldstart")
		}
		args := flag.Args()
		if len(args) == 0 {
			fatalf("command arguments after '--' are required for coldstart")
		}
		res, err := runColdStart(args[0], args[1:], *workdir, *probeURL, *probeMode, *expectID, *valkeyURL, *loginUser, *loginPass, *trials, *timeout)
		if err != nil {
			fatalf("coldstart failed: %v", err)
		}
		printJSON(res)
	case "feed":
		res, err := runFeedBench(*baseURL, *loginUser, *loginPass, *warmup, *requests, *concurrency, *limit, *expectedCount)
		if err != nil {
			fatalf("feed benchmark failed: %v", err)
		}
		printJSON(res)
	case "nlp":
		res := runNLPBench(*nlpWarmup, *nlpIters, defaultVISample, defaultENSample)
		printJSON(res)
	default:
		fatalf("unknown -mode=%q", *mode)
	}
}

func validateProbeBody(probeMode, expectID string, body []byte) error {
	switch probeMode {
	case "", "plain":
		return nil
	case "v2-readyz", "v2-deps":
		var payload struct {
			Status   string `json:"status"`
			Postgres string `json:"postgres"`
			Valkey   string `json:"valkey"`
		}
		if err := json.Unmarshal(body, &payload); err != nil {
			return fmt.Errorf("invalid v2-readyz JSON: %w", err)
		}
		if payload.Status != "ready" || payload.Postgres != "ok" || payload.Valkey != "ok" {
			return fmt.Errorf("v2-readyz not ready: status=%q postgres=%q valkey=%q", payload.Status, payload.Postgres, payload.Valkey)
		}
		return nil
	case "legacy-profile", "legacy-deps":
		var payload struct {
			ID    string            `json:"id"`
			Bio   *string           `json:"bio"`
			Posts []json.RawMessage `json:"posts"`
		}
		if err := json.Unmarshal(body, &payload); err != nil {
			return fmt.Errorf("invalid legacy-profile JSON: %w", err)
		}
		if payload.ID == "" {
			return errors.New("legacy-profile missing id")
		}
		if expectID != "" && payload.ID != expectID {
			return fmt.Errorf("legacy-profile id=%q, want %q", payload.ID, expectID)
		}
		if payload.Bio != nil && *payload.Bio == "Fullstack Developer & NLP enthusiast" {
			return errors.New("legacy-profile returned synthetic DB-error fallback")
		}
		if len(payload.Posts) == 0 {
			return errors.New("legacy-profile returned 0 posts (expected DB-backed seeded posts)")
		}
		return nil
	default:
		return fmt.Errorf("unknown -probe-mode=%q", probeMode)
	}
}

func probeOnce(client *http.Client, probeURL, probeMode, expectID string) error {
	resp, err := client.Get(probeURL)
	if err != nil {
		return err
	}
	body, readErr := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if readErr != nil {
		return fmt.Errorf("read probe body: %w", readErr)
	}
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	return validateProbeBody(probeMode, expectID, body)
}

func decodeJWTPayloadClaims(token string, dst any) error {
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return errors.New("invalid JWT segment count")
	}
	raw, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return fmt.Errorf("decode JWT payload: %w", err)
	}
	return json.Unmarshal(raw, dst)
}

func verifyLegacyValkeyRoundTrip(client *http.Client, probeURL, expectID, valkeyURL, loginUser, loginPass string) error {
	if valkeyURL == "" {
		return errors.New("-valkey-url is required for -probe-mode=legacy-deps")
	}
	if expectID == "" {
		return errors.New("-expect-id is required for -probe-mode=legacy-deps")
	}
	parsedURL, err := url.Parse(probeURL)
	if err != nil {
		return fmt.Errorf("parse probe URL: %w", err)
	}
	baseURL := fmt.Sprintf("%s://%s", parsedURL.Scheme, parsedURL.Host)

	rdbOpts, err := redis.ParseURL(valkeyURL)
	if err != nil {
		return fmt.Errorf("parse valkey URL: %w", err)
	}
	rdb := redis.NewClient(rdbOpts)
	defer func() { _ = rdb.Close() }()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	// Verify Legacy login hits Postgres (returns seeded expectID, not demo-alice-id-12345)
	// AND writes the exact refresh:<expectID>:<jti> key for this trial's JWT into Valkey.
	payload, _ := json.Marshal(map[string]string{
		"username": loginUser,
		"password": loginPass,
	})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, baseURL+"/api/auth/login", bytes.NewReader(payload))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("legacy login readiness request: %w", err)
	}
	body, readErr := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if readErr != nil || resp.StatusCode != http.StatusOK {
		return fmt.Errorf("legacy login readiness HTTP %d readErr=%v", resp.StatusCode, readErr)
	}
	var loginParsed struct {
		UserID       string `json:"userId"`
		AccessToken  string `json:"accessToken"`
		RefreshToken string `json:"refreshToken"`
	}
	if err := json.Unmarshal(body, &loginParsed); err != nil {
		return fmt.Errorf("decode legacy login readiness JSON: %w", err)
	}
	if loginParsed.UserID != expectID || loginParsed.AccessToken == "" || loginParsed.RefreshToken == "" {
		return fmt.Errorf("legacy login returned userId=%q (want %q)", loginParsed.UserID, expectID)
	}

	var rtClaims struct {
		JTI string `json:"jti"`
	}
	if err := decodeJWTPayloadClaims(loginParsed.RefreshToken, &rtClaims); err != nil || rtClaims.JTI == "" {
		return fmt.Errorf("extract jti from legacy refreshToken: err=%v jti=%q", err, rtClaims.JTI)
	}

	exactKey := fmt.Sprintf("refresh:%s:%s", expectID, rtClaims.JTI)
	stored, err := rdb.Get(ctx, exactKey).Result()
	if err != nil {
		return fmt.Errorf("query exact valkey key %s: %w", exactKey, err)
	}
	if stored != rtClaims.JTI {
		return fmt.Errorf("unexpected valkey value for %s: got %q, want %q", exactKey, stored, rtClaims.JTI)
	}
	return nil
}

func verifyV2ValkeyRoundTrip(client *http.Client, probeURL, expectID, valkeyURL, loginUser, loginPass string) error {
	if valkeyURL == "" {
		return errors.New("-valkey-url is required for -probe-mode=v2-deps")
	}
	if expectID == "" {
		return errors.New("-expect-id is required for -probe-mode=v2-deps")
	}
	parsedURL, err := url.Parse(probeURL)
	if err != nil {
		return fmt.Errorf("parse probe URL: %w", err)
	}
	baseURL := fmt.Sprintf("%s://%s", parsedURL.Scheme, parsedURL.Host)

	rdbOpts, err := redis.ParseURL(valkeyURL)
	if err != nil {
		return fmt.Errorf("parse valkey URL: %w", err)
	}
	rdb := redis.NewClient(rdbOpts)
	defer func() { _ = rdb.Close() }()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	payload, _ := json.Marshal(map[string]string{
		"username": loginUser,
		"password": loginPass,
	})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, baseURL+"/api/auth/login", bytes.NewReader(payload))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("v2 login readiness request: %w", err)
	}
	body, readErr := io.ReadAll(resp.Body)
	cookies := resp.Cookies()
	_ = resp.Body.Close()
	if readErr != nil || resp.StatusCode != http.StatusOK {
		return fmt.Errorf("v2 login readiness HTTP %d readErr=%v", resp.StatusCode, readErr)
	}

	var loginParsed struct {
		AccessToken string `json:"accessToken"`
		User        struct {
			ID string `json:"id"`
		} `json:"user"`
	}
	if err := json.Unmarshal(body, &loginParsed); err != nil {
		return fmt.Errorf("decode v2 login readiness JSON: %w", err)
	}
	if loginParsed.User.ID != expectID || loginParsed.AccessToken == "" {
		return fmt.Errorf("v2 login returned user.id=%q (want %q)", loginParsed.User.ID, expectID)
	}

	var refreshCookieVal string
	for _, c := range cookies {
		if c.Name == "refreshToken" && c.Value != "" {
			refreshCookieVal = c.Value
			break
		}
	}
	if refreshCookieVal == "" {
		return errors.New("v2 login response missing refreshToken cookie")
	}

	var rtClaims struct {
		Fam string `json:"fam"`
		JTI string `json:"jti"`
	}
	if err := decodeJWTPayloadClaims(refreshCookieVal, &rtClaims); err != nil || rtClaims.Fam == "" || rtClaims.JTI == "" {
		return fmt.Errorf("extract fam/jti from v2 refreshToken: err=%v fam=%q jti=%q", err, rtClaims.Fam, rtClaims.JTI)
	}

	exactKey := "refresh:family:" + rtClaims.Fam
	stored, err := rdb.Get(ctx, exactKey).Result()
	if err != nil {
		return fmt.Errorf("query exact valkey key %s: %w", exactKey, err)
	}
	wantPrefix := fmt.Sprintf("%s:%s:", expectID, rtClaims.JTI)
	if !strings.HasPrefix(stored, wantPrefix) {
		return fmt.Errorf("unexpected valkey value for %s: got %q (want prefix %q)", exactKey, stored, wantPrefix)
	}
	return nil
}

func waitReady(probeURL, probeMode, expectID, valkeyURL, loginUser, loginPass string, timeout time.Duration) error {
	client := &http.Client{
		Timeout: 500 * time.Millisecond,
		Transport: &http.Transport{
			DisableKeepAlives: true,
		},
	}
	deadline := time.Now().Add(timeout)
	var lastErr error
	for time.Now().Before(deadline) {
		if err := probeOnce(client, probeURL, probeMode, expectID); err == nil {
			switch probeMode {
			case "legacy-deps":
				if depErr := verifyLegacyValkeyRoundTrip(client, probeURL, expectID, valkeyURL, loginUser, loginPass); depErr != nil {
					lastErr = depErr
					time.Sleep(5 * time.Millisecond)
					continue
				}
			case "v2-deps":
				if depErr := verifyV2ValkeyRoundTrip(client, probeURL, expectID, valkeyURL, loginUser, loginPass); depErr != nil {
					lastErr = depErr
					time.Sleep(5 * time.Millisecond)
					continue
				}
			}
			return nil
		} else {
			lastErr = err
		}
		time.Sleep(1 * time.Millisecond)
	}
	return fmt.Errorf("timeout after %s (last=%v)", timeout, lastErr)
}

func runColdStart(bin string, args []string, workdir, probeURL, probeMode, expectID, valkeyURL, loginUser, loginPass string, trials int, timeout time.Duration) (*coldStartResult, error) {
	if trials <= 0 {
		return nil, errors.New("trials must be > 0")
	}
	client := &http.Client{
		Timeout: 500 * time.Millisecond,
		Transport: &http.Transport{
			DisableKeepAlives: true,
		},
	}

	var cleanRDB *redis.Client
	if probeMode == "legacy-deps" || probeMode == "v2-deps" {
		if valkeyURL == "" {
			return nil, fmt.Errorf("-valkey-url is required for -probe-mode=%s", probeMode)
		}
		opts, err := redis.ParseURL(valkeyURL)
		if err != nil {
			return nil, fmt.Errorf("parse -valkey-url: %w", err)
		}
		cleanRDB = redis.NewClient(opts)
		defer func() { _ = cleanRDB.Close() }()
	}

	results := make([]float64, 0, trials)
	for i := 0; i < trials; i++ {
		if cleanRDB != nil {
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			flushErr := cleanRDB.FlushDB(ctx).Err()
			cancel()
			if flushErr != nil {
				return nil, fmt.Errorf("trial %d pretrial valkey FlushDB failed: %w", i+1, flushErr)
			}
		}

		var stderrBuf bytes.Buffer
		// nosemgrep: go.lang.security.audit.dangerous-exec-command.dangerous-exec-command
		cmd := exec.Command(bin, args...) // #nosec G204 -- The operator selects the local benchmark executable and arguments.
		cmd.Dir = workdir
		cmd.Stdout = io.Discard
		cmd.Stderr = &stderrBuf

		t0 := time.Now()
		if err := cmd.Start(); err != nil {
			return nil, fmt.Errorf("trial %d start: %w", i+1, err)
		}

		waitDone := make(chan error, 1)
		go func() {
			waitDone <- cmd.Wait()
		}()

		deadline := t0.Add(timeout)
		ready := false
		var lastErr error

		for time.Now().Before(deadline) {
			select {
			case err := <-waitDone:
				return nil, fmt.Errorf("trial %d process exited early (%v): %s", i+1, err, strings.TrimSpace(stderrBuf.String()))
			default:
			}

			if err := probeOnce(client, probeURL, probeMode, expectID); err == nil {
				switch probeMode {
				case "legacy-deps":
					if depErr := verifyLegacyValkeyRoundTrip(client, probeURL, expectID, valkeyURL, loginUser, loginPass); depErr != nil {
						lastErr = depErr
						time.Sleep(5 * time.Millisecond)
						continue
					}
				case "v2-deps":
					if depErr := verifyV2ValkeyRoundTrip(client, probeURL, expectID, valkeyURL, loginUser, loginPass); depErr != nil {
						lastErr = depErr
						time.Sleep(5 * time.Millisecond)
						continue
					}
				}
				elapsedMs := float64(time.Since(t0).Microseconds()) / 1000.0
				results = append(results, round3(elapsedMs))
				ready = true
				break
			} else {
				lastErr = err
			}
			time.Sleep(1 * time.Millisecond)
		}

		if cmd.Process != nil {
			_ = cmd.Process.Signal(syscall.SIGTERM)
			select {
			case <-waitDone:
			case <-time.After(3 * time.Second):
				_ = cmd.Process.Kill()
				<-waitDone
			}
		}

		if !ready {
			return nil, fmt.Errorf("trial %d readiness timeout after %s (last=%v, stderr=%s)", i+1, timeout, lastErr, strings.TrimSpace(stderrBuf.String()))
		}
		time.Sleep(250 * time.Millisecond)
	}

	sorted := append([]float64(nil), results...)
	sort.Float64s(sorted)
	return &coldStartResult{
		TrialsMs: results,
		MinMs:    sorted[0],
		MedianMs: percentileSorted(sorted, 0.50),
		MaxMs:    sorted[len(sorted)-1],
	}, nil
}

func runNLPBench(warmup, iters int, viSample, enSample string) *nlpResult {
	if warmup < 0 {
		warmup = 0
	}
	if iters <= 0 {
		iters = 500
	}

	// Measure initial call / table warm latency rather than hardcoding zero.
	tInit0 := time.Now()
	_ = lexicon.Score("initialization probe")
	initMs := round4(float64(time.Since(tInit0).Nanoseconds()) / 1e6)

	for i := 0; i < warmup; i++ {
		_ = lexicon.Score(viSample)
		_ = lexicon.Score(enSample)
	}

	measure := func(sample string) nlpSampleStats {
		times := make([]float64, 0, iters)
		var label lexicon.Sentiment
		var sum float64
		for i := 0; i < iters; i++ {
			t0 := time.Now()
			label = lexicon.Score(sample)
			ms := float64(time.Since(t0).Nanoseconds()) / 1e6
			times = append(times, ms)
			sum += ms
		}
		sort.Float64s(times)
		return nlpSampleStats{
			Warmup: warmup,
			Iters:  iters,
			MeanMs: round4(sum / float64(len(times))),
			P50Ms:  round4(percentileFloorSorted(times, 0.50)),
			P95Ms:  round4(percentileFloorSorted(times, 0.95)),
			MinMs:  round4(times[0]),
			MaxMs:  round4(times[len(times)-1]),
			Label:  string(label),
		}
	}

	return &nlpResult{
		InitMs: initMs,
		VI:     measure(viSample),
		EN:     measure(enSample),
	}
}

func runFeedBench(baseURL, username, password string, warmup, requests, concurrency, limit, expectedCount int) (*feedResult, error) {
	if concurrency <= 0 {
		concurrency = 1
	}
	if requests <= 0 {
		return nil, errors.New("requests must be > 0")
	}

	baseURL = strings.TrimRight(baseURL, "/")
	transport := &http.Transport{
		MaxIdleConns:        concurrency * 4,
		MaxIdleConnsPerHost: concurrency * 4,
		IdleConnTimeout:     30 * time.Second,
	}
	client := &http.Client{
		Timeout:   15 * time.Second,
		Transport: transport,
	}

	token, err := login(client, baseURL, username, password)
	if err != nil {
		return nil, err
	}

	feedURL := fmt.Sprintf("%s/api/posts?limit=%d", baseURL, limit)

	// Warmup
	if warmup > 0 {
		if _, _, _, err := executeBatch(client, feedURL, token, warmup, concurrency, expectedCount); err != nil {
			return nil, fmt.Errorf("warmup failed: %w", err)
		}
	}

	// Measured run
	latencies, totalDur, errCount, err := executeBatch(client, feedURL, token, requests, concurrency, expectedCount)
	if err != nil {
		return nil, fmt.Errorf("measured batch failed: %w", err)
	}
	if errCount > 0 {
		return nil, fmt.Errorf("recorded %d failed requests out of %d", errCount, requests)
	}

	sort.Float64s(latencies)
	var sum float64
	for _, v := range latencies {
		sum += v
	}
	totalMs := float64(totalDur.Microseconds()) / 1000.0
	rps := float64(len(latencies)) / totalDur.Seconds()

	return &feedResult{
		BaseURL:       baseURL,
		Concurrency:   concurrency,
		Warmup:        warmup,
		Requests:      requests,
		OKCount:       int64(len(latencies)),
		ErrCount:      errCount,
		ExpectedCount: expectedCount,
		TotalMs:       round3(totalMs),
		RPS:           round2(rps),
		MinMs:         latencies[0],
		MeanMs:        round3(sum / float64(len(latencies))),
		P50Ms:         percentileSorted(latencies, 0.50),
		P95Ms:         percentileSorted(latencies, 0.95),
		P99Ms:         percentileSorted(latencies, 0.99),
		MaxMs:         latencies[len(latencies)-1],
	}, nil
}

func login(client *http.Client, baseURL, username, password string) (string, error) {
	payload, _ := json.Marshal(map[string]string{
		"username": username,
		"password": password,
	})
	req, err := http.NewRequestWithContext(context.Background(), http.MethodPost, baseURL+"/api/auth/login", bytes.NewReader(payload))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("login request: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("read login response: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("login HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}

	var parsed struct {
		AccessToken string `json:"accessToken"`
	}
	if err := json.Unmarshal(body, &parsed); err != nil {
		return "", fmt.Errorf("decode login JSON: %w", err)
	}
	if parsed.AccessToken == "" {
		return "", errors.New("login response missing accessToken")
	}
	return parsed.AccessToken, nil
}

func executeBatch(client *http.Client, feedURL, token string, count, concurrency, expectedCount int) ([]float64, time.Duration, int64, error) {
	jobs := make(chan struct{}, count)
	for i := 0; i < count; i++ {
		jobs <- struct{}{}
	}
	close(jobs)

	var mu sync.Mutex
	latencies := make([]float64, 0, count)
	var errCount atomic.Int64
	var firstErr error
	var wg sync.WaitGroup

	startAll := time.Now()
	for w := 0; w < concurrency; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for range jobs {
				reqStart := time.Now()
				req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, feedURL, nil)
				if err != nil {
					errCount.Add(1)
					mu.Lock()
					if firstErr == nil {
						firstErr = err
					}
					mu.Unlock()
					continue
				}
				req.Header.Set("Authorization", "Bearer "+token)

				resp, err := client.Do(req)
				if err != nil {
					errCount.Add(1)
					mu.Lock()
					if firstErr == nil {
						firstErr = err
					}
					mu.Unlock()
					continue
				}
				body, readErr := io.ReadAll(resp.Body)
				_ = resp.Body.Close()
				elapsedMs := round3(float64(time.Since(reqStart).Microseconds()) / 1000.0)

				if readErr != nil || resp.StatusCode != http.StatusOK {
					errCount.Add(1)
					mu.Lock()
					if firstErr == nil {
						firstErr = fmt.Errorf("HTTP %d readErr=%v body=%s", resp.StatusCode, readErr, strings.TrimSpace(string(body)))
					}
					mu.Unlock()
					continue
				}

				if expectedCount >= 0 {
					var parsed struct {
						Data []json.RawMessage `json:"data"`
					}
					if uErr := json.Unmarshal(body, &parsed); uErr != nil || len(parsed.Data) != expectedCount {
						errCount.Add(1)
						mu.Lock()
						if firstErr == nil {
							firstErr = fmt.Errorf("unexpected data count %d (want %d), unmarshalErr=%v", len(parsed.Data), expectedCount, uErr)
						}
						mu.Unlock()
						continue
					}
				}

				mu.Lock()
				latencies = append(latencies, elapsedMs)
				mu.Unlock()
			}
		}()
	}
	wg.Wait()
	totalDur := time.Since(startAll)
	return latencies, totalDur, errCount.Load(), firstErr
}

func percentileSorted(sorted []float64, p float64) float64 {
	if len(sorted) == 0 {
		return 0
	}
	if len(sorted) == 1 {
		return sorted[0]
	}
	idx := int(math.Ceil(p*float64(len(sorted)))) - 1
	if idx < 0 {
		idx = 0
	}
	if idx >= len(sorted) {
		idx = len(sorted) - 1
	}
	return sorted[idx]
}

// percentileFloorSorted matches the Legacy Node driver's Math.floor(p * (n - 1)) estimator.
func percentileFloorSorted(sorted []float64, p float64) float64 {
	if len(sorted) == 0 {
		return 0
	}
	if len(sorted) == 1 {
		return sorted[0]
	}
	idx := int(math.Floor(p * float64(len(sorted)-1)))
	if idx < 0 {
		idx = 0
	}
	if idx >= len(sorted) {
		idx = len(sorted) - 1
	}
	return sorted[idx]
}

func round2(v float64) float64 {
	return math.Round(v*100) / 100
}

func round3(v float64) float64 {
	return math.Round(v*1000) / 1000
}

func round4(v float64) float64 {
	return math.Round(v*10000) / 10000
}

func printJSON(v any) {
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	if err := enc.Encode(v); err != nil {
		fatalf("encode json: %v", err)
	}
}

func fatalf(format string, args ...any) {
	fmt.Fprintf(os.Stderr, "benchload: "+format+"\n", args...)
	os.Exit(1)
}
