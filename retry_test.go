package bungkus

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

func TestRetries(t *testing.T) {
	var calls atomic.Int32
	// fail answers with status for the first n calls, then succeeds.
	fail := func(n int32, status int, header ...string) string {
		calls.Store(0)
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if calls.Add(1) <= n {
				for i := 0; i+1 < len(header); i += 2 {
					w.Header().Set(header[i], header[i+1])
				}
				w.WriteHeader(status)
				return
			}
			w.Write([]byte(`{"choices":[{"message":{"content":"ok"}}]}`))
		}))
		t.Cleanup(srv.Close)
		return srv.URL
	}
	chat := func(url string, opts ...Option) error {
		_, err := NewOpenAI("k", append([]Option{WithBaseURL(url)}, opts...)...).Chat(context.Background(), &ChatRequest{})
		return err
	}

	if err := chat(fail(2, 429, "retry-after-ms", "1")); err != nil || calls.Load() != 3 {
		t.Errorf("429 twice: %v after %d calls", err, calls.Load())
	}
	if err := chat(fail(1, 503, "Retry-After", "0")); err != nil || calls.Load() != 2 {
		t.Errorf("503 once: %v after %d calls", err, calls.Load())
	}
	var apiErr *APIError
	if err := chat(fail(3, 500)); !errors.As(err, &apiErr) || calls.Load() != 3 {
		t.Errorf("500 three times: want APIError after 3 calls, got %v after %d", err, calls.Load())
	}
	if err := chat(fail(1, 400)); !errors.As(err, &apiErr) || calls.Load() != 1 {
		t.Errorf("400 must not retry: %v after %d calls", err, calls.Load())
	}
	if err := chat(fail(1, 429), WithRetries(0)); err == nil || calls.Load() != 1 {
		t.Errorf("WithRetries(0): %v after %d calls", err, calls.Load())
	}

	// A cancelled context stops the wait between attempts.
	ctx, cancel := context.WithCancel(context.Background())
	url := fail(5, 429, "Retry-After", "60")
	go func() { time.Sleep(20 * time.Millisecond); cancel() }()
	if _, err := NewOpenAI("k", WithBaseURL(url)).Chat(ctx, &ChatRequest{}); !errors.Is(err, context.Canceled) {
		t.Errorf("cancel during wait: %v", err)
	}
}

func TestRetryAfter(t *testing.T) {
	h := http.Header{}
	if retryAfter(h) != 0 {
		t.Error("no header should mean 0")
	}
	h.Set("Retry-After", time.Now().Add(time.Hour).UTC().Format(http.TimeFormat))
	if d := retryAfter(h); d < 59*time.Minute {
		t.Errorf("HTTP date: %v", d)
	}
	h.Set("Retry-After", "3")
	if retryAfter(h) != 3*time.Second {
		t.Error("seconds")
	}
	h.Set("retry-after-ms", "250")
	if retryAfter(h) != 250*time.Millisecond {
		t.Error("retry-after-ms wins")
	}
}

func TestBackoffRange(t *testing.T) {
	for attempt, base := range map[int]time.Duration{0: 500 * time.Millisecond, 1: time.Second, 2: 2 * time.Second, 10: 8 * time.Second} {
		for range 50 {
			if d := defaultBackoff(attempt); d < base*3/4 || d >= base*5/4 {
				t.Fatalf("attempt %d: %v outside [%v, %v)", attempt, d, base*3/4, base*5/4)
			}
		}
	}
}
