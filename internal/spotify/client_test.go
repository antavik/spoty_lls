package spotify

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

// ---- test constants mirroring internal/config values (not imported: tests must
// stay independent of config so URL/timing assumptions are explicit here). ----
const (
	testMaxRetries   = 3
	testRetryDelay   = 2 * time.Second
	testMaxRetryWait = 300 * time.Second
	testRequestTO    = 15 * time.Second
)

// ---- helpers -------------------------------------------------------------

// sleepRecorder captures durations passed to the injected c.sleep func
// without ever actually sleeping.
type sleepRecorder struct {
	mu    sync.Mutex
	waits []time.Duration
}

func (s *sleepRecorder) record(d time.Duration) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.waits = append(s.waits, d)
}

func (s *sleepRecorder) durations() []time.Duration {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]time.Duration, len(s.waits))
	copy(out, s.waits)
	return out
}

func allZero(ds []time.Duration) bool {
	for _, d := range ds {
		if d != 0 {
			return false
		}
	}
	return true
}

func equalDurations(a, b []time.Duration) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// reqLog is one recorded HTTP request hitting a test server.
type reqLog struct {
	Method string
	Path   string
	Query  string
	Auth   string
	Body   []byte
}

type requestRecorder struct {
	mu   sync.Mutex
	reqs []reqLog
}

func (r *requestRecorder) middleware(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, req *http.Request) {
		body, _ := io.ReadAll(req.Body)
		r.mu.Lock()
		r.reqs = append(r.reqs, reqLog{
			Method: req.Method,
			Path:   req.URL.Path,
			Query:  req.URL.RawQuery,
			Auth:   req.Header.Get("Authorization"),
			Body:   body,
		})
		r.mu.Unlock()
		next(w, req)
	}
}

func (r *requestRecorder) all() []reqLog {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]reqLog, len(r.reqs))
	copy(out, r.reqs)
	return out
}

func (r *requestRecorder) count() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.reqs)
}

// respStep is one canned response in a scripted sequence. Once the sequence
// is exhausted, the handler keeps repeating the last step.
type respStep struct {
	status int
	header map[string]string
	body   string
}

func serveSteps(steps []respStep) http.HandlerFunc {
	var mu sync.Mutex
	i := 0
	return func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		idx := i
		if i < len(steps)-1 {
			i++
		}
		mu.Unlock()
		st := steps[idx]
		for k, v := range st.header {
			w.Header().Set(k, v)
		}
		w.WriteHeader(st.status)
		if st.body != "" {
			_, _ = w.Write([]byte(st.body))
		}
	}
}

// newTestClient builds a *Client (via New) pointed at srv for both the API
// base and the token endpoint, with an injected non-sleeping sleep func and
// a preset bearer token ("tok") so API calls under test don't need to
// authenticate first unless the subtest is exercising auth itself.
func newTestClient(t *testing.T, srv *httptest.Server) (*Client, *sleepRecorder) {
	t.Helper()
	c, err := New("id", "secret", "refresh")
	if err != nil {
		t.Fatalf("New() unexpected error: %v", err)
	}
	c.apiBase = srv.URL
	c.tokenURL = srv.URL + "/token"
	rec := &sleepRecorder{}
	c.sleep = rec.record
	c.token = "tok"
	return c, rec
}

func errContains(t *testing.T, err error, substr string) {
	t.Helper()
	if err == nil {
		t.Fatalf("expected error containing %q, got nil", substr)
	}
	if !strings.Contains(err.Error(), substr) {
		t.Fatalf("expected error containing %q, got %q", substr, err.Error())
	}
}

// ---- TestNew --------------------------------------------------------------

func TestNew(t *testing.T) {
	t.Run("empty refresh token errors", func(t *testing.T) {
		c, err := New("id", "secret", "")
		if err == nil {
			t.Fatalf("expected error for empty refresh token, got nil (client=%v)", c)
		}
	})

	t.Run("valid inputs produce sane defaults", func(t *testing.T) {
		c, err := New("id", "secret", "refresh")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if c == nil {
			t.Fatal("expected non-nil client")
		}
		if c.clientID != "id" || c.clientSecret != "secret" || c.refreshToken != "refresh" {
			t.Fatalf("credentials not stored correctly: %+v", c)
		}
		if c.token != "" {
			t.Fatalf("expected empty token before Authenticate, got %q", c.token)
		}
		if c.sleep == nil {
			t.Fatal("expected non-nil sleep func")
		}
		if c.http == nil {
			t.Fatal("expected non-nil http client")
		}
		if c.http.Timeout != testRequestTO {
			t.Fatalf("expected http.Timeout=%v, got %v", testRequestTO, c.http.Timeout)
		}
		if c.apiBase == "" {
			t.Fatal("expected non-empty apiBase default")
		}
		if c.tokenURL == "" {
			t.Fatal("expected non-empty tokenURL default")
		}
	})
}

// ---- TestAuthenticate -------------------------------------------------------

func TestAuthenticate(t *testing.T) {
	t.Run("success sets token and sends correct request", func(t *testing.T) {
		rec := &requestRecorder{}
		mux := http.NewServeMux()
		mux.HandleFunc("/token", rec.middleware(serveSteps([]respStep{
			{status: 200, body: `{"access_token":"ABC"}`},
		})))
		srv := httptest.NewServer(mux)
		defer srv.Close()

		c, err := New("id", "secret", "refresh")
		if err != nil {
			t.Fatalf("New() error: %v", err)
		}
		c.tokenURL = srv.URL + "/token"

		if err := c.Authenticate(); err != nil {
			t.Fatalf("Authenticate() unexpected error: %v", err)
		}
		if c.token != "ABC" {
			t.Fatalf("expected token=ABC, got %q", c.token)
		}

		reqs := rec.all()
		if len(reqs) != 1 {
			t.Fatalf("expected exactly 1 token request, got %d", len(reqs))
		}
		wantAuth := "Basic " + base64.StdEncoding.EncodeToString([]byte("id:secret"))
		if reqs[0].Auth != wantAuth {
			t.Fatalf("expected Authorization %q, got %q", wantAuth, reqs[0].Auth)
		}
		bodyStr := string(reqs[0].Body)
		if !strings.Contains(bodyStr, "grant_type=refresh_token") {
			t.Fatalf("expected form body to contain grant_type=refresh_token, got %q", bodyStr)
		}
		if !strings.Contains(bodyStr, "refresh_token=refresh") {
			t.Fatalf("expected form body to contain refresh_token=refresh, got %q", bodyStr)
		}
	})

	t.Run("non-2xx response errors with token refresh failed", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(`{"error":"invalid_grant"}`))
		}))
		defer srv.Close()

		c, err := New("id", "secret", "refresh")
		if err != nil {
			t.Fatalf("New() error: %v", err)
		}
		c.tokenURL = srv.URL

		err = c.Authenticate()
		errContains(t, err, "token refresh failed")
		errContains(t, err, "400")
	})

	t.Run("invalid JSON body errors", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`not-json`))
		}))
		defer srv.Close()

		c, err := New("id", "secret", "refresh")
		if err != nil {
			t.Fatalf("New() error: %v", err)
		}
		c.tokenURL = srv.URL

		if err := c.Authenticate(); err == nil {
			t.Fatal("expected error for invalid JSON response, got nil")
		}
	})

	t.Run("missing access_token errors", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"token_type":"Bearer"}`))
		}))
		defer srv.Close()

		c, err := New("id", "secret", "refresh")
		if err != nil {
			t.Fatalf("New() error: %v", err)
		}
		c.tokenURL = srv.URL

		if err := c.Authenticate(); err == nil {
			t.Fatal("expected error for missing access_token, got nil")
		}
	})

	t.Run("token propagates as Bearer header on subsequent API calls", func(t *testing.T) {
		rec := &requestRecorder{}
		mux := http.NewServeMux()
		mux.HandleFunc("/token", serveSteps([]respStep{
			{status: 200, body: `{"access_token":"ABC"}`},
		}))
		mux.HandleFunc("/me", rec.middleware(serveSteps([]respStep{
			{status: 200, body: `{"id":"user1"}`},
		})))
		srv := httptest.NewServer(mux)
		defer srv.Close()

		c, err := New("id", "secret", "refresh")
		if err != nil {
			t.Fatalf("New() error: %v", err)
		}
		c.apiBase = srv.URL
		c.tokenURL = srv.URL + "/token"
		c.sleep = func(time.Duration) {}

		if err := c.Authenticate(); err != nil {
			t.Fatalf("Authenticate() unexpected error: %v", err)
		}
		id, err := c.CurrentUserID()
		if err != nil {
			t.Fatalf("CurrentUserID() unexpected error: %v", err)
		}
		if id != "user1" {
			t.Fatalf("expected id=user1, got %q", id)
		}
		reqs := rec.all()
		if len(reqs) != 1 {
			t.Fatalf("expected exactly 1 /me request, got %d", len(reqs))
		}
		if reqs[0].Auth != "Bearer ABC" {
			t.Fatalf("expected Authorization %q, got %q", "Bearer ABC", reqs[0].Auth)
		}
	})
}

// ---- TestDoRetry: retry/backoff contract exercised via CurrentUserID/ChangeDetails ----

func TestDoRetry(t *testing.T) {
	t.Run("401 then success re-authenticates and retries immediately", func(t *testing.T) {
		meRec := &requestRecorder{}
		mux := http.NewServeMux()
		mux.HandleFunc("/token", serveSteps([]respStep{
			{status: 200, body: `{"access_token":"newtok"}`},
		}))
		mux.HandleFunc("/me", meRec.middleware(serveSteps([]respStep{
			{status: 401},
			{status: 200, body: `{"id":"user1"}`},
		})))
		srv := httptest.NewServer(mux)
		defer srv.Close()

		c, rec := newTestClient(t, srv)

		id, err := c.CurrentUserID()
		if err != nil {
			t.Fatalf("CurrentUserID() unexpected error: %v", err)
		}
		if id != "user1" {
			t.Fatalf("expected id=user1, got %q", id)
		}
		reqs := meRec.all()
		if len(reqs) != 2 {
			t.Fatalf("expected 2 /me requests, got %d", len(reqs))
		}
		if reqs[1].Auth != "Bearer newtok" {
			t.Fatalf("expected retry Authorization %q, got %q", "Bearer newtok", reqs[1].Auth)
		}
		if !allZero(rec.durations()) {
			t.Fatalf("expected no positive sleep for 401 retry, got %v", rec.durations())
		}
	})

	t.Run("401 exhausted after max retries errors", func(t *testing.T) {
		meRec := &requestRecorder{}
		mux := http.NewServeMux()
		mux.HandleFunc("/token", serveSteps([]respStep{
			{status: 200, body: `{"access_token":"newtok"}`},
		}))
		mux.HandleFunc("/me", meRec.middleware(serveSteps([]respStep{
			{status: 401},
		})))
		srv := httptest.NewServer(mux)
		defer srv.Close()

		c, rec := newTestClient(t, srv)

		_, err := c.CurrentUserID()
		if err == nil {
			t.Fatal("expected error after exhausting retries on persistent 401, got nil")
		}
		if meRec.count() != testMaxRetries {
			t.Fatalf("expected %d /me attempts, got %d", testMaxRetries, meRec.count())
		}
		if !allZero(rec.durations()) {
			t.Fatalf("expected no positive sleep for 401 retries, got %v", rec.durations())
		}
	})

	t.Run("429 honors Retry-After header", func(t *testing.T) {
		meRec := &requestRecorder{}
		mux := http.NewServeMux()
		mux.HandleFunc("/me", meRec.middleware(serveSteps([]respStep{
			{status: 429, header: map[string]string{"Retry-After": "7"}},
			{status: 200, body: `{"id":"user1"}`},
		})))
		srv := httptest.NewServer(mux)
		defer srv.Close()

		c, rec := newTestClient(t, srv)

		if _, err := c.CurrentUserID(); err != nil {
			t.Fatalf("CurrentUserID() unexpected error: %v", err)
		}
		want := []time.Duration{7 * time.Second}
		if !equalDurations(rec.durations(), want) {
			t.Fatalf("expected sleeps %v, got %v", want, rec.durations())
		}
	})

	t.Run("429 Retry-After capped at max wait", func(t *testing.T) {
		meRec := &requestRecorder{}
		mux := http.NewServeMux()
		mux.HandleFunc("/me", meRec.middleware(serveSteps([]respStep{
			{status: 429, header: map[string]string{"Retry-After": "600"}},
			{status: 200, body: `{"id":"user1"}`},
		})))
		srv := httptest.NewServer(mux)
		defer srv.Close()

		c, rec := newTestClient(t, srv)

		if _, err := c.CurrentUserID(); err != nil {
			t.Fatalf("CurrentUserID() unexpected error: %v", err)
		}
		want := []time.Duration{testMaxRetryWait}
		if !equalDurations(rec.durations(), want) {
			t.Fatalf("expected capped sleeps %v, got %v", want, rec.durations())
		}
	})

	t.Run("429 unparsable Retry-After defaults to retry delay", func(t *testing.T) {
		meRec := &requestRecorder{}
		mux := http.NewServeMux()
		mux.HandleFunc("/me", meRec.middleware(serveSteps([]respStep{
			{status: 429, header: map[string]string{"Retry-After": "abc"}},
			{status: 200, body: `{"id":"user1"}`},
		})))
		srv := httptest.NewServer(mux)
		defer srv.Close()

		c, rec := newTestClient(t, srv)

		if _, err := c.CurrentUserID(); err != nil {
			t.Fatalf("CurrentUserID() unexpected error: %v", err)
		}
		want := []time.Duration{testRetryDelay}
		if !equalDurations(rec.durations(), want) {
			t.Fatalf("expected default sleeps %v, got %v", want, rec.durations())
		}
	})

	t.Run("429 without Retry-After defaults to retry delay", func(t *testing.T) {
		meRec := &requestRecorder{}
		mux := http.NewServeMux()
		mux.HandleFunc("/me", meRec.middleware(serveSteps([]respStep{
			{status: 429},
			{status: 200, body: `{"id":"user1"}`},
		})))
		srv := httptest.NewServer(mux)
		defer srv.Close()

		c, rec := newTestClient(t, srv)

		if _, err := c.CurrentUserID(); err != nil {
			t.Fatalf("CurrentUserID() unexpected error: %v", err)
		}
		want := []time.Duration{testRetryDelay}
		if !equalDurations(rec.durations(), want) {
			t.Fatalf("expected default sleeps %v, got %v", want, rec.durations())
		}
	})

	t.Run("5xx then success retries with retry delay", func(t *testing.T) {
		meRec := &requestRecorder{}
		mux := http.NewServeMux()
		mux.HandleFunc("/me", meRec.middleware(serveSteps([]respStep{
			{status: 500},
			{status: 200, body: `{"id":"user1"}`},
		})))
		srv := httptest.NewServer(mux)
		defer srv.Close()

		c, rec := newTestClient(t, srv)

		if _, err := c.CurrentUserID(); err != nil {
			t.Fatalf("CurrentUserID() unexpected error: %v", err)
		}
		want := []time.Duration{testRetryDelay}
		if !equalDurations(rec.durations(), want) {
			t.Fatalf("expected sleeps %v, got %v", want, rec.durations())
		}
	})

	t.Run("5xx exhausted after max retries errors", func(t *testing.T) {
		meRec := &requestRecorder{}
		mux := http.NewServeMux()
		mux.HandleFunc("/me", meRec.middleware(serveSteps([]respStep{
			{status: 500},
		})))
		srv := httptest.NewServer(mux)
		defer srv.Close()

		c, rec := newTestClient(t, srv)

		_, err := c.CurrentUserID()
		if err == nil {
			t.Fatal("expected error after exhausting retries on persistent 500, got nil")
		}
		if meRec.count() != testMaxRetries {
			t.Fatalf("expected %d attempts, got %d", testMaxRetries, meRec.count())
		}
		want := []time.Duration{testRetryDelay, testRetryDelay}
		if !equalDurations(rec.durations(), want) {
			t.Fatalf("expected sleeps %v, got %v", want, rec.durations())
		}
	})

	t.Run("other 4xx errors immediately without retry", func(t *testing.T) {
		meRec := &requestRecorder{}
		mux := http.NewServeMux()
		mux.HandleFunc("/me", meRec.middleware(serveSteps([]respStep{
			{status: 404},
		})))
		srv := httptest.NewServer(mux)
		defer srv.Close()

		c, rec := newTestClient(t, srv)

		_, err := c.CurrentUserID()
		if err == nil {
			t.Fatal("expected immediate error on 404, got nil")
		}
		if meRec.count() != 1 {
			t.Fatalf("expected exactly 1 request for non-retryable 404, got %d", meRec.count())
		}
		if len(rec.durations()) != 0 {
			t.Fatalf("expected no sleep for non-retryable 404, got %v", rec.durations())
		}
	})

	t.Run("network error retries then errors", func(t *testing.T) {
		// A server that is closed before use guarantees connection failures
		// on every attempt, simulating a network error at the transport level.
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			t.Fatal("handler should never be invoked: server was closed before use")
		}))
		closedURL := srv.URL
		srv.Close()

		c, err := New("id", "secret", "refresh")
		if err != nil {
			t.Fatalf("New() error: %v", err)
		}
		c.apiBase = closedURL
		c.tokenURL = closedURL + "/token"
		rec := &sleepRecorder{}
		c.sleep = rec.record
		c.token = "tok"

		_, err = c.CurrentUserID()
		if err == nil {
			t.Fatal("expected error after network failures, got nil")
		}
		want := []time.Duration{testRetryDelay, testRetryDelay}
		if !equalDurations(rec.durations(), want) {
			t.Fatalf("expected sleeps %v, got %v", want, rec.durations())
		}
	})

	t.Run("empty 2xx body is treated as success", func(t *testing.T) {
		putRec := &requestRecorder{}
		mux := http.NewServeMux()
		mux.HandleFunc("/playlists/pl1", putRec.middleware(serveSteps([]respStep{
			{status: 200, body: ""},
		})))
		srv := httptest.NewServer(mux)
		defer srv.Close()

		c, _ := newTestClient(t, srv)

		if err := c.ChangeDetails("pl1", "desc"); err != nil {
			t.Fatalf("ChangeDetails() unexpected error on empty 2xx body: %v", err)
		}
		if putRec.count() != 1 {
			t.Fatalf("expected exactly 1 PUT request, got %d", putRec.count())
		}
	})

	t.Run("invalid JSON 2xx body errors", func(t *testing.T) {
		mux := http.NewServeMux()
		mux.HandleFunc("/me", serveSteps([]respStep{
			{status: 200, body: "not-json"},
		}))
		srv := httptest.NewServer(mux)
		defer srv.Close()

		c, _ := newTestClient(t, srv)

		if _, err := c.CurrentUserID(); err == nil {
			t.Fatal("expected error for invalid JSON 2xx body, got nil")
		}
	})

	t.Run("unauthenticated client errors without making a request", func(t *testing.T) {
		globalRec := &requestRecorder{}
		mux := http.NewServeMux()
		mux.HandleFunc("/", globalRec.middleware(serveSteps([]respStep{
			{status: 200, body: `{"id":"user1"}`},
		})))
		srv := httptest.NewServer(mux)
		defer srv.Close()

		c, _ := newTestClient(t, srv)
		c.token = ""

		_, err := c.CurrentUserID()
		errContains(t, err, "not authenticated")
		if globalRec.count() != 0 {
			t.Fatalf("expected zero requests for unauthenticated call, got %d", globalRec.count())
		}
	})
}

// ---- TestCurrentUserID ------------------------------------------------------

func TestCurrentUserID(t *testing.T) {
	t.Run("returns id on success", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path != "/me" {
				t.Fatalf("expected path /me, got %s", r.URL.Path)
			}
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"id":"user1"}`))
		}))
		defer srv.Close()

		c, _ := newTestClient(t, srv)

		id, err := c.CurrentUserID()
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if id != "user1" {
			t.Fatalf("expected id=user1, got %q", id)
		}
	})

	t.Run("missing id errors", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{}`))
		}))
		defer srv.Close()

		c, _ := newTestClient(t, srv)

		if _, err := c.CurrentUserID(); err == nil {
			t.Fatal("expected error for missing id, got nil")
		}
	})
}

// ---- TestLikedTrackURIs ------------------------------------------------------

func TestLikedTrackURIs(t *testing.T) {
	t.Run("follows pagination and collects uris in order", func(t *testing.T) {
		var srv *httptest.Server
		hits := 0
		var mu sync.Mutex
		mux := http.NewServeMux()
		mux.HandleFunc("/me/tracks", func(w http.ResponseWriter, r *http.Request) {
			mu.Lock()
			hits++
			n := hits
			mu.Unlock()
			w.WriteHeader(http.StatusOK)
			if n == 1 {
				next := srv.URL + "/me/tracks?page=2"
				_, _ = w.Write([]byte(fmt.Sprintf(`{
					"items": [
						{"track": {"uri": "spotify:track:1"}},
						{"track": {"uri": "spotify:track:2"}}
					],
					"next": %q
				}`, next)))
				return
			}
			_, _ = w.Write([]byte(`{
				"items": [
					{"track": {"uri": "spotify:track:3"}},
					{"track": {"uri": "spotify:track:4"}}
				],
				"next": null
			}`))
		})
		srv = httptest.NewServer(mux)
		defer srv.Close()

		c, _ := newTestClient(t, srv)

		uris, err := c.LikedTrackURIs(100)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		want := []string{"spotify:track:1", "spotify:track:2", "spotify:track:3", "spotify:track:4"}
		if len(uris) != len(want) {
			t.Fatalf("expected %d uris, got %d (%v)", len(want), len(uris), uris)
		}
		for i, u := range want {
			if uris[i] != u {
				t.Fatalf("uri[%d]: expected %q, got %q", i, u, uris[i])
			}
		}
		mu.Lock()
		gotHits := hits
		mu.Unlock()
		if gotHits != 2 {
			t.Fatalf("expected exactly 2 requests (next=null terminates), got %d", gotHits)
		}
	})

	t.Run("stops early once limit reached", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{
				"items": [
					{"track": {"uri": "spotify:track:1"}},
					{"track": {"uri": "spotify:track:2"}},
					{"track": {"uri": "spotify:track:3"}}
				],
				"next": null
			}`))
		}))
		defer srv.Close()

		c, _ := newTestClient(t, srv)

		uris, err := c.LikedTrackURIs(2)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		want := []string{"spotify:track:1", "spotify:track:2"}
		if len(uris) != len(want) {
			t.Fatalf("expected %d uris, got %d (%v)", len(want), len(uris), uris)
		}
		for i, u := range want {
			if uris[i] != u {
				t.Fatalf("uri[%d]: expected %q, got %q", i, u, uris[i])
			}
		}
	})

	t.Run("skips items with missing track or uri", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{
				"items": [
					{"track": null},
					{"track": {}},
					{"track": {"uri": "spotify:track:x"}}
				],
				"next": null
			}`))
		}))
		defer srv.Close()

		c, _ := newTestClient(t, srv)

		uris, err := c.LikedTrackURIs(10)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		want := []string{"spotify:track:x"}
		if len(uris) != len(want) || uris[0] != want[0] {
			t.Fatalf("expected %v, got %v", want, uris)
		}
	})
}

// ---- TestFindPlaylist ------------------------------------------------------

func TestFindPlaylist(t *testing.T) {
	t.Run("finds playlist matching name and owner", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{
				"items": [
					{"id": "other1", "name": "Other", "owner": {"id": "u1"}, "description": "x"},
					{"id": "other2", "name": "Last Liked", "owner": {"id": "u2"}, "description": "y"},
					{"id": "pl1", "name": "Last Liked", "owner": {"id": "u1"}, "description": "desc [#abcdef012345]"}
				],
				"next": null
			}`))
		}))
		defer srv.Close()

		c, _ := newTestClient(t, srv)

		id, desc, found, err := c.FindPlaylist("Last Liked", "u1")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if !found {
			t.Fatal("expected found=true")
		}
		if id != "pl1" {
			t.Fatalf("expected id=pl1, got %q", id)
		}
		if desc != "desc [#abcdef012345]" {
			t.Fatalf("expected description, got %q", desc)
		}
	})

	t.Run("null description becomes empty string", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{
				"items": [
					{"id": "pl1", "name": "Last Liked", "owner": {"id": "u1"}, "description": null}
				],
				"next": null
			}`))
		}))
		defer srv.Close()

		c, _ := newTestClient(t, srv)

		id, desc, found, err := c.FindPlaylist("Last Liked", "u1")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if !found || id != "pl1" {
			t.Fatalf("expected found pl1, got found=%v id=%q", found, id)
		}
		if desc != "" {
			t.Fatalf("expected empty description for null, got %q", desc)
		}
	})

	t.Run("no match returns found=false", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{
				"items": [
					{"id": "other1", "name": "Other", "owner": {"id": "u1"}, "description": ""}
				],
				"next": null
			}`))
		}))
		defer srv.Close()

		c, _ := newTestClient(t, srv)

		id, desc, found, err := c.FindPlaylist("Last Liked", "u1")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if found {
			t.Fatalf("expected found=false, got true (id=%q desc=%q)", id, desc)
		}
		if id != "" || desc != "" {
			t.Fatalf("expected empty id/desc on no match, got id=%q desc=%q", id, desc)
		}
	})

	t.Run("follows pagination via next", func(t *testing.T) {
		var srv *httptest.Server
		mux := http.NewServeMux()
		mux.HandleFunc("/me/playlists", func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusOK)
			if r.URL.RawQuery == "page=2" {
				_, _ = w.Write([]byte(`{
					"items": [
						{"id": "pl1", "name": "Last Liked", "owner": {"id": "u1"}, "description": "d"}
					],
					"next": null
				}`))
				return
			}
			next := srv.URL + "/me/playlists?page=2"
			_, _ = w.Write([]byte(fmt.Sprintf(`{
				"items": [
					{"id": "other1", "name": "Other", "owner": {"id": "u1"}, "description": ""}
				],
				"next": %q
			}`, next)))
		})
		srv = httptest.NewServer(mux)
		defer srv.Close()

		c, _ := newTestClient(t, srv)

		id, desc, found, err := c.FindPlaylist("Last Liked", "u1")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if !found || id != "pl1" || desc != "d" {
			t.Fatalf("expected found pl1/d via pagination, got found=%v id=%q desc=%q", found, id, desc)
		}
	})
}

// ---- TestCreatePlaylist -----------------------------------------------------

func TestCreatePlaylist(t *testing.T) {
	t.Run("posts to owner playlists endpoint with expected body", func(t *testing.T) {
		rec := &requestRecorder{}
		mux := http.NewServeMux()
		mux.HandleFunc("/users/u1/playlists", rec.middleware(serveSteps([]respStep{
			{status: 200, body: `{"id":"pl9"}`},
		})))
		srv := httptest.NewServer(mux)
		defer srv.Close()

		c, _ := newTestClient(t, srv)

		id, err := c.CreatePlaylist("u1", "Last Liked")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if id != "pl9" {
			t.Fatalf("expected id=pl9, got %q", id)
		}

		reqs := rec.all()
		if len(reqs) != 1 {
			t.Fatalf("expected exactly 1 request, got %d", len(reqs))
		}
		if reqs[0].Method != http.MethodPost {
			t.Fatalf("expected POST, got %s", reqs[0].Method)
		}
		if reqs[0].Path != "/users/u1/playlists" {
			t.Fatalf("expected path /users/u1/playlists, got %s", reqs[0].Path)
		}
		var payload map[string]interface{}
		if err := json.Unmarshal(reqs[0].Body, &payload); err != nil {
			t.Fatalf("failed to decode request body %q: %v", reqs[0].Body, err)
		}
		if payload["name"] != "Last Liked" {
			t.Fatalf("expected name=Last Liked in body, got %v", payload["name"])
		}
		if pub, ok := payload["public"].(bool); !ok || pub != false {
			t.Fatalf("expected public=false in body, got %v", payload["public"])
		}
	})

	t.Run("missing id in response errors", func(t *testing.T) {
		mux := http.NewServeMux()
		mux.HandleFunc("/users/u1/playlists", serveSteps([]respStep{
			{status: 200, body: `{}`},
		}))
		srv := httptest.NewServer(mux)
		defer srv.Close()

		c, _ := newTestClient(t, srv)

		if _, err := c.CreatePlaylist("u1", "Last Liked"); err == nil {
			t.Fatal("expected error for missing id, got nil")
		}
	})
}

// ---- TestChangeDetails ------------------------------------------------------

func TestChangeDetails(t *testing.T) {
	t.Run("empty description makes no request", func(t *testing.T) {
		rec := &requestRecorder{}
		mux := http.NewServeMux()
		mux.HandleFunc("/playlists/pl1", rec.middleware(serveSteps([]respStep{
			{status: 200, body: `{}`},
		})))
		srv := httptest.NewServer(mux)
		defer srv.Close()

		c, _ := newTestClient(t, srv)

		if err := c.ChangeDetails("pl1", ""); err != nil {
			t.Fatalf("unexpected error for empty description: %v", err)
		}
		if rec.count() != 0 {
			t.Fatalf("expected zero requests for empty description, got %d", rec.count())
		}
	})

	t.Run("non-empty description sends PUT with only description field", func(t *testing.T) {
		rec := &requestRecorder{}
		mux := http.NewServeMux()
		mux.HandleFunc("/playlists/pl1", rec.middleware(serveSteps([]respStep{
			{status: 200, body: `{}`},
		})))
		srv := httptest.NewServer(mux)
		defer srv.Close()

		c, _ := newTestClient(t, srv)

		if err := c.ChangeDetails("pl1", "d"); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		reqs := rec.all()
		if len(reqs) != 1 {
			t.Fatalf("expected exactly 1 request, got %d", len(reqs))
		}
		if reqs[0].Method != http.MethodPut {
			t.Fatalf("expected PUT, got %s", reqs[0].Method)
		}
		if reqs[0].Path != "/playlists/pl1" {
			t.Fatalf("expected path /playlists/pl1, got %s", reqs[0].Path)
		}
		var payload map[string]interface{}
		if err := json.Unmarshal(reqs[0].Body, &payload); err != nil {
			t.Fatalf("failed to decode request body %q: %v", reqs[0].Body, err)
		}
		if len(payload) != 1 {
			t.Fatalf("expected exactly 1 field in body, got %d (%v)", len(payload), payload)
		}
		if payload["description"] != "d" {
			t.Fatalf("expected description=d, got %v", payload["description"])
		}
	})
}

// ---- TestReplaceTracks -------------------------------------------------------

type urisBody struct {
	Uris []string `json:"uris"`
}

func TestReplaceTracks(t *testing.T) {
	t.Run("empty uris makes no request", func(t *testing.T) {
		rec := &requestRecorder{}
		mux := http.NewServeMux()
		mux.HandleFunc("/playlists/pl1/tracks", rec.middleware(serveSteps([]respStep{
			{status: 200, body: `{"snapshot_id":"snap"}`},
		})))
		srv := httptest.NewServer(mux)
		defer srv.Close()

		c, _ := newTestClient(t, srv)

		if err := c.ReplaceTracks("pl1", []string{}); err != nil {
			t.Fatalf("unexpected error for empty uris: %v", err)
		}
		if rec.count() != 0 {
			t.Fatalf("expected zero requests for empty uris, got %d", rec.count())
		}
	})

	t.Run("exactly 100 uris uses a single PUT and no POST", func(t *testing.T) {
		uris := make([]string, 100)
		for i := range uris {
			uris[i] = fmt.Sprintf("uri-%03d", i)
		}
		rec := &requestRecorder{}
		mux := http.NewServeMux()
		mux.HandleFunc("/playlists/pl1/tracks", rec.middleware(serveSteps([]respStep{
			{status: 200, body: `{"snapshot_id":"snap"}`},
		})))
		srv := httptest.NewServer(mux)
		defer srv.Close()

		c, _ := newTestClient(t, srv)

		if err := c.ReplaceTracks("pl1", uris); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		reqs := rec.all()
		if len(reqs) != 1 {
			t.Fatalf("expected exactly 1 request for 100 uris, got %d", len(reqs))
		}
		if reqs[0].Method != http.MethodPut {
			t.Fatalf("expected PUT, got %s", reqs[0].Method)
		}
	})

	t.Run("more than 100 uris chunks with PUT then POSTs, preserving order", func(t *testing.T) {
		total := 250
		uris := make([]string, total)
		for i := range uris {
			uris[i] = fmt.Sprintf("uri-%03d", i)
		}
		rec := &requestRecorder{}
		mux := http.NewServeMux()
		mux.HandleFunc("/playlists/pl1/tracks", rec.middleware(serveSteps([]respStep{
			{status: 200, body: `{"snapshot_id":"snap"}`},
		})))
		srv := httptest.NewServer(mux)
		defer srv.Close()

		c, _ := newTestClient(t, srv)

		if err := c.ReplaceTracks("pl1", uris); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		reqs := rec.all()
		if len(reqs) < 2 {
			t.Fatalf("expected at least 2 requests to chunk %d uris, got %d", total, len(reqs))
		}
		if reqs[0].Method != http.MethodPut {
			t.Fatalf("expected first request to be PUT, got %s", reqs[0].Method)
		}
		if reqs[0].Path != "/playlists/pl1/tracks" {
			t.Fatalf("expected path /playlists/pl1/tracks, got %s", reqs[0].Path)
		}
		for i := 1; i < len(reqs); i++ {
			if reqs[i].Method != http.MethodPost {
				t.Fatalf("expected request[%d] to be POST, got %s", i, reqs[i].Method)
			}
			if reqs[i].Path != "/playlists/pl1/tracks" {
				t.Fatalf("expected path /playlists/pl1/tracks, got %s", reqs[i].Path)
			}
		}

		var got []string
		for i, r := range reqs {
			var b urisBody
			if err := json.Unmarshal(r.Body, &b); err != nil {
				t.Fatalf("failed to decode chunk %d body %q: %v", i, r.Body, err)
			}
			if len(b.Uris) > 100 {
				t.Fatalf("chunk %d exceeds max batch size 100: got %d", i, len(b.Uris))
			}
			got = append(got, b.Uris...)
		}
		if len(got) != total {
			t.Fatalf("expected %d total uris across chunks, got %d", total, len(got))
		}
		for i, u := range uris {
			if got[i] != u {
				t.Fatalf("uri[%d]: expected %q, got %q (order/completeness broken)", i, u, got[i])
			}
		}
	})

	t.Run("batch boundaries do not panic and chunk correctly", func(t *testing.T) {
		cases := []struct {
			total    int
			wantReqs int
		}{
			{1, 1},
			{99, 1},
			{101, 2},
			{200, 2},
		}

		for _, tc := range cases {
			t.Run(fmt.Sprintf("total_%d", tc.total), func(t *testing.T) {
				uris := make([]string, tc.total)
				for i := range uris {
					uris[i] = fmt.Sprintf("uri-%03d", i)
				}
				rec := &requestRecorder{}
				mux := http.NewServeMux()
				mux.HandleFunc("/playlists/pl1/tracks", rec.middleware(serveSteps([]respStep{
					{status: 200, body: `{"snapshot_id":"snap"}`},
				})))
				srv := httptest.NewServer(mux)
				defer srv.Close()

				c, _ := newTestClient(t, srv)

				if err := c.ReplaceTracks("pl1", uris); err != nil {
					t.Fatalf("ReplaceTracks(%d uris) unexpected error: %v", tc.total, err)
				}

				reqs := rec.all()
				if len(reqs) != tc.wantReqs {
					t.Fatalf("expected %d requests for %d uris, got %d", tc.wantReqs, tc.total, len(reqs))
				}
				if reqs[0].Method != http.MethodPut {
					t.Fatalf("expected first request PUT, got %s", reqs[0].Method)
				}
				for i := 1; i < len(reqs); i++ {
					if reqs[i].Method != http.MethodPost {
						t.Fatalf("expected request[%d] POST, got %s", i, reqs[i].Method)
					}
				}

				var got []string
				for i, r := range reqs {
					var b urisBody
					if err := json.Unmarshal(r.Body, &b); err != nil {
						t.Fatalf("failed to decode chunk %d body %q: %v", i, r.Body, err)
					}
					if len(b.Uris) == 0 || len(b.Uris) > 100 {
						t.Fatalf("chunk %d size %d out of bounds (1..100)", i, len(b.Uris))
					}
					got = append(got, b.Uris...)
				}
				if len(got) != tc.total {
					t.Fatalf("expected %d total uris across chunks, got %d", tc.total, len(got))
				}
				for i, u := range uris {
					if got[i] != u {
						t.Fatalf("uri[%d]: expected %q, got %q", i, u, got[i])
					}
				}
			})
		}
	})
}
