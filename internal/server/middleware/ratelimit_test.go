package middleware

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func newTestLimiter() (*RateLimiter, *int, http.Handler) {
	l := NewRateLimiter(1, 2) // 1 req/s, burst of 2
	calls := 0
	h := l.Wrap(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		w.WriteHeader(http.StatusOK)
	}))
	return l, &calls, h
}

func TestRateLimiterAllowsBurstThenRejects(t *testing.T) {
	_, calls, h := newTestLimiter()

	for i := 0; i < 2; i++ {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/", nil))
		if rec.Code != http.StatusOK {
			t.Fatalf("request %d: got %d, want 200", i+1, rec.Code)
		}
	}

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/", nil))
	if rec.Code != http.StatusTooManyRequests {
		t.Fatalf("third request: got %d, want 429", rec.Code)
	}
	if rec.Header().Get("Retry-After") == "" {
		t.Fatal("429 response missing Retry-After header")
	}
	if *calls != 2 {
		t.Fatalf("handler called %d times, want 2", *calls)
	}
}

func TestRateLimiterSeparatesIdentities(t *testing.T) {
	_, _, h := newTestLimiter()

	reqA := func() *http.Request {
		r := httptest.NewRequest(http.MethodPost, "/", nil)
		r.AddCookie(&http.Cookie{Name: "session", Value: "token-a"})
		return r
	}
	reqB := func() *http.Request {
		r := httptest.NewRequest(http.MethodPost, "/", nil)
		r.AddCookie(&http.Cookie{Name: "session", Value: "token-b"})
		return r
	}

	for i := 0; i < 2; i++ {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, reqA())
		if rec.Code != http.StatusOK {
			t.Fatalf("A request %d: got %d, want 200", i+1, rec.Code)
		}
	}

	// A is exhausted, but B must still have its full burst.
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, reqB())
	if rec.Code != http.StatusOK {
		t.Fatalf("B request: got %d, want 200", rec.Code)
	}
}

func TestRateLimiterFallsBackToIP(t *testing.T) {
	_, _, h := newTestLimiter()

	req := func(ip string) *http.Request {
		r := httptest.NewRequest(http.MethodPost, "/", nil)
		r.RemoteAddr = ip
		return r
	}

	for i := 0; i < 2; i++ {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req("1.2.3.4:5000"))
		if rec.Code != http.StatusOK {
			t.Fatalf("request %d: got %d, want 200", i+1, rec.Code)
		}
	}

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req("1.2.3.4:5001"))
	if rec.Code != http.StatusTooManyRequests {
		t.Fatalf("same IP new port: got %d, want 429", rec.Code)
	}

	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, req("9.9.9.9:5000"))
	if rec.Code != http.StatusOK {
		t.Fatalf("different IP: got %d, want 200", rec.Code)
	}
}

func TestRateLimiterIgnoresReads(t *testing.T) {
	l := NewRateLimiter(1, 1)
	calls := 0
	h := l.Wrap(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		w.WriteHeader(http.StatusOK)
	}))

	for i := 0; i < 10; i++ {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))
		if rec.Code != http.StatusOK {
			t.Fatalf("GET %d: got %d, want 200", i+1, rec.Code)
		}
	}
	if calls != 10 {
		t.Fatalf("handler called %d times, want 10", calls)
	}

	// The write bucket was never touched by the GETs.
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("POST after GETs: got %d, want 200", rec.Code)
	}
}
