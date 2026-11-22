package main

import (
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestPercentileSorted(t *testing.T) {
	vals := []float64{1, 2, 3, 4, 5, 6, 7, 8, 9, 10}
	if got := percentileSorted(vals, 0.50); got != 5 {
		t.Fatalf("p50 = %v, want 5", got)
	}
	if got := percentileSorted(vals, 0.95); got != 10 {
		t.Fatalf("p95 = %v, want 10", got)
	}
	if got := percentileSorted(nil, 0.50); got != 0 {
		t.Fatalf("empty p50 = %v, want 0", got)
	}
}

func TestValidateProbeBody(t *testing.T) {
	// v2-readyz requires status=ready, postgres=ok, valkey=ok
	if err := validateProbeBody("v2-readyz", "", []byte(`{"status":"ready","postgres":"ok","valkey":"ok"}`)); err != nil {
		t.Fatalf("expected valid v2-readyz to pass, got %v", err)
	}
	if err := validateProbeBody("v2-readyz", "", []byte(`{"status":"not_ready","postgres":"error","valkey":"ok"}`)); err == nil {
		t.Fatal("expected degraded v2-readyz to fail")
	}

	// legacy-profile rejects synthetic 200 fallback returned when Prisma fails
	const aliceID = "11111111-1111-1111-1111-111111111111"
	syntheticFallback := `{"id":"11111111-1111-1111-1111-111111111111","bio":"Fullstack Developer & NLP enthusiast","posts":[]}`
	if err := validateProbeBody("legacy-profile", aliceID, []byte(syntheticFallback)); err == nil {
		t.Fatal("expected synthetic legacy-profile fallback to be rejected")
	}

	// legacy-profile accepts genuine DB-backed profile with seeded posts
	realLegacy := `{"id":"11111111-1111-1111-1111-111111111111","bio":null,"posts":[{"id":"p1"}]}`
	if err := validateProbeBody("legacy-profile", aliceID, []byte(realLegacy)); err != nil {
		t.Fatalf("expected real DB-backed legacy-profile to pass, got %v", err)
	}
	if err := validateProbeBody("legacy-deps", aliceID, []byte(realLegacy)); err != nil {
		t.Fatalf("expected real DB-backed legacy-deps to pass, got %v", err)
	}
}

func TestVerifyLegacyValkeyRoundTripFailsWhenValkeyUnreachable(t *testing.T) {
	const aliceID = "11111111-1111-1111-1111-111111111111"
	payloadBytes, _ := json.Marshal(map[string]string{"jti": "jti-trial-1"})
	fakeJWT := "eyJhbGciOiJIUzI1NiJ9." + base64.RawURLEncoding.EncodeToString(payloadBytes) + ".sig"

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/auth/login" {
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]string{
				"userId":       aliceID,
				"accessToken":  "tok-legacy",
				"refreshToken": fakeJWT,
			})
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()

	err := verifyLegacyValkeyRoundTrip(srv.Client(), srv.URL+"/api/auth/profile/"+aliceID, aliceID, "redis://127.0.0.1:1/0", "alice@example.com", "password")
	if err == nil {
		t.Fatal("expected verifyLegacyValkeyRoundTrip to fail when Valkey is unreachable")
	}

	var decoded struct {
		JTI string `json:"jti"`
	}
	if err := decodeJWTPayloadClaims(fakeJWT, &decoded); err != nil || decoded.JTI != "jti-trial-1" {
		t.Fatalf("decodeJWTPayloadClaims: err=%v jti=%q", err, decoded.JTI)
	}
}

func TestRunNLPBenchLikeForLike(t *testing.T) {
	res := runNLPBench(10, 50, defaultVISample, defaultENSample)
	if res.VI.Iters != 50 || res.EN.Iters != 50 {
		t.Fatalf("unexpected iters: %+v", res)
	}
	if res.VI.Label != "GOOD" || res.EN.Label != "GOOD" {
		t.Fatalf("expected GOOD labels, got vi=%s en=%s", res.VI.Label, res.EN.Label)
	}
	if res.VI.P50Ms > res.VI.P95Ms || res.EN.P50Ms > res.EN.P95Ms {
		t.Fatalf("expected p50 <= p95, got %+v", res)
	}
}

func TestRunFeedBenchAgainstHTTPTestServer(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/readyz":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"status":"ready","postgres":"ok","valkey":"ok"}`))
		case "/api/auth/login":
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]string{"accessToken": "tok-123"})
		case "/api/posts":
			if r.Header.Get("Authorization") != "Bearer tok-123" {
				w.WriteHeader(http.StatusUnauthorized)
				return
			}
			w.Header().Set("Content-Type", "application/json")
			items := make([]map[string]string, 10)
			for i := range items {
				items[i] = map[string]string{"id": "p"}
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"data": items})
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer srv.Close()

	if err := waitReady(srv.URL+"/readyz", "v2-readyz", "", "", "", "", 2*time.Second); err != nil {
		t.Fatalf("waitReady(/readyz): %v", err)
	}

	res, err := runFeedBench(srv.URL, "alice@example.com", "password", 5, 20, 4, 10, 10)
	if err != nil {
		t.Fatalf("runFeedBench: %v", err)
	}
	if res.OKCount != 20 || res.ErrCount != 0 {
		t.Fatalf("unexpected counts: %+v", res)
	}
}
