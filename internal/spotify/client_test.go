package spotify

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
	"unicode/utf8"

	"spoty_lls/internal/helpers"
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

// roundTripFunc counts each round trip and delegates to a scriptable fn,
// simulating transport failures without real network I/O so tests can assert
// exact call counts.
type roundTripFunc struct {
	calls int
	fn    func(*http.Request) (*http.Response, error)
}

func (f *roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	f.calls++
	return f.fn(req)
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

// ---- TestDoRetry: retry/backoff contract exercised via CurrentUserID/CreatePlaylist/ChangeDetails ----

func TestDoRetry(t *testing.T) {
	const (
		oldAuth = "Bearer tok"
		newAuth = "Bearer newtok"
	)
	getMe := func(c *Client) error { _, err := c.CurrentUserID(); return err }
	createPlaylist := func(c *Client) error { _, err := c.CreatePlaylist("u1", "Last Liked"); return err }
	changeDetails := func(c *Client) error { return c.ChangeDetails("pl1", "d") }
	retryAfter := func(v string) map[string]string { return map[string]string{"Retry-After": v} }
	tokenOK := respStep{status: 200, body: `{"access_token":"newtok"}`}
	meOK := respStep{status: 200, body: `{"id":"user1"}`}
	playlistOK := respStep{status: 200, body: `{"id":"pl42"}`}

	// A POST (CreatePlaylist, ReplaceTracks append) may already have taken
	// effect server-side on a 5xx, so it is NOT retried (A-80). 401 and 429
	// are retried for every method. Every HTTP failure carries the raw
	// Spotify response body in the error (A-83).
	cases := []struct {
		name       string
		path       string
		call       func(*Client) error
		steps      []respStep
		token      respStep // /token reply; zero value registers no /token handler
		wantErr    string   // substring; "" means success
		wantAuths  []string // Authorization header of each API request (len = attempts)
		wantSleeps []time.Duration
	}{
		{
			name: "401 re-authenticates and retries immediately", path: "/me", call: getMe,
			steps: []respStep{{status: 401}, meOK}, token: tokenOK,
			wantAuths: []string{oldAuth, newAuth},
		},
		{
			name: "401 exhausted returns response body", path: "/me", call: getMe,
			steps: []respStep{{status: 401, body: `{"error":"invalid_token"}`}}, token: tokenOK,
			wantErr:   `HTTP 401: retrying after token refresh: {"error":"invalid_token"}`,
			wantAuths: []string{oldAuth, newAuth, newAuth},
		},
		{
			name: "401 with failed token refresh stops without retry", path: "/me", call: getMe,
			steps: []respStep{{status: 401}}, token: respStep{status: 400, body: `{"error":"invalid_grant"}`},
			wantErr:   `token refresh failed: HTTP 400: {"error":"invalid_grant"}`,
			wantAuths: []string{oldAuth},
		},
		{
			name: "429 honors Retry-After", path: "/me", call: getMe,
			steps:      []respStep{{status: 429, header: retryAfter("7")}, meOK},
			wantAuths:  []string{oldAuth, oldAuth},
			wantSleeps: []time.Duration{7 * time.Second},
		},
		{
			name: "429 Retry-After capped at max wait", path: "/me", call: getMe,
			steps:      []respStep{{status: 429, header: retryAfter("600")}, meOK},
			wantAuths:  []string{oldAuth, oldAuth},
			wantSleeps: []time.Duration{testMaxRetryWait},
		},
		{
			name: "429 unparsable Retry-After defaults to retry delay", path: "/me", call: getMe,
			steps:      []respStep{{status: 429, header: retryAfter("abc")}, meOK},
			wantAuths:  []string{oldAuth, oldAuth},
			wantSleeps: []time.Duration{testRetryDelay},
		},
		{
			name: "429 without Retry-After defaults to retry delay", path: "/me", call: getMe,
			steps:      []respStep{{status: 429}, meOK},
			wantAuths:  []string{oldAuth, oldAuth},
			wantSleeps: []time.Duration{testRetryDelay},
		},
		{
			name: "429 exhausted returns response body", path: "/me", call: getMe,
			steps:      []respStep{{status: 429, header: retryAfter("7"), body: `{"error":"rate_limited"}`}},
			wantErr:    `HTTP 429: {"error":"rate_limited"}`,
			wantAuths:  []string{oldAuth, oldAuth, oldAuth},
			wantSleeps: []time.Duration{7 * time.Second, 7 * time.Second},
		},
		{
			name: "5xx retries with retry delay", path: "/me", call: getMe,
			steps:      []respStep{{status: 500}, meOK},
			wantAuths:  []string{oldAuth, oldAuth},
			wantSleeps: []time.Duration{testRetryDelay},
		},
		{
			name: "5xx exhausted returns response body", path: "/me", call: getMe,
			steps:      []respStep{{status: 503, body: "upstream down"}},
			wantErr:    "HTTP 503: upstream down",
			wantAuths:  []string{oldAuth, oldAuth, oldAuth},
			wantSleeps: []time.Duration{testRetryDelay, testRetryDelay},
		},
		{
			name: "other 4xx fails immediately with response body", path: "/me", call: getMe,
			steps:     []respStep{{status: 404, body: `{"error":"not found"}`}},
			wantErr:   `HTTP 404: {"error":"not found"}`,
			wantAuths: []string{oldAuth},
		},
		{
			name: "5xx on POST is not retried (outcome unknown)", path: "/users/u1/playlists", call: createPlaylist,
			steps:     []respStep{{status: 500, body: "boom"}},
			wantErr:   "HTTP 500: boom",
			wantAuths: []string{oldAuth},
		},
		{
			name: "401 on POST re-authenticates and retries", path: "/users/u1/playlists", call: createPlaylist,
			steps: []respStep{{status: 401}, playlistOK}, token: tokenOK,
			wantAuths: []string{oldAuth, newAuth},
		},
		{
			name: "429 on POST honors Retry-After and retries", path: "/users/u1/playlists", call: createPlaylist,
			steps:      []respStep{{status: 429, header: retryAfter("5")}, playlistOK},
			wantAuths:  []string{oldAuth, oldAuth},
			wantSleeps: []time.Duration{5 * time.Second},
		},
		{
			name: "5xx on PUT is retried (idempotent)", path: "/playlists/pl1", call: changeDetails,
			steps:      []respStep{{status: 500}, {status: 200}},
			wantAuths:  []string{oldAuth, oldAuth},
			wantSleeps: []time.Duration{testRetryDelay},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			apiRec := &requestRecorder{}
			mux := http.NewServeMux()
			mux.HandleFunc(tc.path, apiRec.middleware(serveSteps(tc.steps)))
			if tc.token.status != 0 {
				mux.HandleFunc("/token", serveSteps([]respStep{tc.token}))
			}
			srv := httptest.NewServer(mux)
			defer srv.Close()

			c, rec := newTestClient(t, srv)
			err := tc.call(c)

			if tc.wantErr == "" {
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
			} else {
				errContains(t, err, tc.wantErr)
			}
			var auths []string
			for _, r := range apiRec.all() {
				auths = append(auths, r.Auth)
			}
			if !slices.Equal(auths, tc.wantAuths) {
				t.Errorf("request Authorization headers = %q, want %q", auths, tc.wantAuths)
			}
			if got := rec.durations(); !slices.Equal(got, tc.wantSleeps) {
				t.Errorf("sleeps = %v, want %v", got, tc.wantSleeps)
			}
		})
	}

	t.Run("network error", func(t *testing.T) {
		cases := []struct {
			name       string
			call       func(*Client) error
			wantCalls  int
			wantSleeps []time.Duration
		}{
			{"GET is retried", getMe, testMaxRetries, []time.Duration{testRetryDelay, testRetryDelay}},
			{"POST is not retried (outcome unknown)", createPlaylist, 1, nil},
		}
		for _, tc := range cases {
			t.Run(tc.name, func(t *testing.T) {
				srv := httptest.NewServer(http.NotFoundHandler())
				defer srv.Close()

				c, rec := newTestClient(t, srv)
				rt := &roundTripFunc{fn: func(*http.Request) (*http.Response, error) {
					return nil, fmt.Errorf("connection reset by peer")
				}}
				c.http.Transport = rt

				errContains(t, tc.call(c), "network error")
				if rt.calls != tc.wantCalls {
					t.Errorf("round trips = %d, want %d", rt.calls, tc.wantCalls)
				}
				if got := rec.durations(); !slices.Equal(got, tc.wantSleeps) {
					t.Errorf("sleeps = %v, want %v", got, tc.wantSleeps)
				}
			})
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
	// Also pins 2xx body decoding in do(): an empty body leaves the target
	// zero-valued, and a non-JSON body is echoed in the error.
	cases := []struct {
		name    string
		body    string
		wantID  string
		wantErr string
	}{
		{name: "returns id", body: `{"id":"user1"}`, wantID: "user1"},
		{name: "missing id errors", body: `{}`, wantErr: "missing 'id'"},
		{name: "empty body errors as missing id", body: "", wantErr: "missing 'id'"},
		{name: "invalid JSON errors with raw body", body: "not-json", wantErr: "invalid JSON response: not-json"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != http.MethodGet || r.URL.Path != "/me" {
					t.Errorf("request = %s %s, want GET /me", r.Method, r.URL.Path)
				}
				_, _ = w.Write([]byte(tc.body))
			}))
			defer srv.Close()

			c, _ := newTestClient(t, srv)
			id, err := c.CurrentUserID()

			if tc.wantErr != "" {
				errContains(t, err, tc.wantErr)
			} else if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if id != tc.wantID {
				t.Errorf("id = %q, want %q", id, tc.wantID)
			}
		})
	}
}

// ---- TestLikedTracks ---------------------------------------------------------

func TestLikedTracks(t *testing.T) {
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

		tracks, err := c.LikedTracks(100)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		uris := helpers.URIs(tracks)
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

	t.Run("single page decoding", func(t *testing.T) {
		cases := []struct {
			name    string
			body    string
			limit   int
			want    []helpers.Track
			wantErr string
		}{
			{
				name:  "stops early once limit reached",
				body:  `{"items":[{"track":{"uri":"spotify:track:1"}},{"track":{"uri":"spotify:track:2"}},{"track":{"uri":"spotify:track:3"}}],"next":null}`,
				limit: 2,
				want:  []helpers.Track{{URI: "spotify:track:1"}, {URI: "spotify:track:2"}},
			},
			{
				name:  "skips items with missing track or uri",
				body:  `{"items":[{"track":null},{"track":{}},{"track":{"uri":"spotify:track:x"}}],"next":null}`,
				limit: 10,
				want:  []helpers.Track{{URI: "spotify:track:x"}},
			},
			{
				name: "collects title and non-empty artist names",
				body: `{"items":[
					{"track":{"uri":"spotify:track:1","name":"Get Lucky","artists":[{"name":"Daft Punk"},{"name":"Pharrell Williams"},{}]}},
					{"track":{"uri":"spotify:track:2"}}
				],"next":null}`,
				limit: 10,
				want: []helpers.Track{
					{URI: "spotify:track:1", Title: "Get Lucky", Artists: []string{"Daft Punk", "Pharrell Williams"}},
					{URI: "spotify:track:2"},
				},
			},
			{
				name:    "wrongly typed field fails instead of syncing a partial list",
				body:    `{"items":[{"track":{"uri":"spotify:track:1"}},{"track":{"uri":42}}],"next":null}`,
				limit:   10,
				wantErr: "unexpected response",
			},
		}
		for _, tc := range cases {
			t.Run(tc.name, func(t *testing.T) {
				srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					_, _ = w.Write([]byte(tc.body))
				}))
				defer srv.Close()

				c, _ := newTestClient(t, srv)
				tracks, err := c.LikedTracks(tc.limit)

				if tc.wantErr != "" {
					errContains(t, err, tc.wantErr)
				} else if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
				if !reflect.DeepEqual(tracks, tc.want) {
					t.Errorf("tracks = %+v, want %+v", tracks, tc.want)
				}
			})
		}
	})
}

// ---- TestFindPlaylist ------------------------------------------------------

func TestFindPlaylist(t *testing.T) {
	t.Run("single page lookup", func(t *testing.T) {
		cases := []struct {
			name      string
			body      string
			wantID    string
			wantDesc  string
			wantFound bool
		}{
			{
				name: "matches name and owner",
				body: `{"items":[
					{"id":"other1","name":"Other","owner":{"id":"u1"},"description":"x"},
					{"id":"other2","name":"Last Liked","owner":{"id":"u2"},"description":"y"},
					{"id":"pl1","name":"Last Liked","owner":{"id":"u1"},"description":"desc [#abcdef012345]"}
				],"next":null}`,
				wantID: "pl1", wantDesc: "desc [#abcdef012345]", wantFound: true,
			},
			{
				name:   "null description becomes empty string",
				body:   `{"items":[{"id":"pl1","name":"Last Liked","owner":{"id":"u1"},"description":null}],"next":null}`,
				wantID: "pl1", wantFound: true,
			},
			{
				name: "complete listing without match is not found",
				body: `{"items":[{"id":"other1","name":"Other","owner":{"id":"u1"},"description":""}],"total":1,"next":null}`,
			},
			{
				name: "complete empty library is not found",
				body: `{"items":[],"total":0,"next":null}`,
			},
			{
				// /me/playlists also lists followed playlists, so another user's
				// "Last Liked" is a real, provably-not-ours entry.
				name: "same-name playlist of another owner is ruled out",
				body: `{"items":[{"id":"foreign","name":"Last Liked","owner":{"id":"u2"},"description":"x"}],"total":1,"next":null}`,
			},
		}
		for _, tc := range cases {
			t.Run(tc.name, func(t *testing.T) {
				srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					_, _ = w.Write([]byte(tc.body))
				}))
				defer srv.Close()

				c, _ := newTestClient(t, srv)
				id, desc, found, err := c.FindPlaylist("Last Liked", "u1")
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
				if id != tc.wantID || desc != tc.wantDesc || found != tc.wantFound {
					t.Errorf("FindPlaylist() = (%q, %q, %v), want (%q, %q, %v)", id, desc, found, tc.wantID, tc.wantDesc, tc.wantFound)
				}
			})
		}
	})
	t.Run("multi-page listing", func(t *testing.T) {
		// NEXT in a page body (and in wantErr) stands for the next page's URL.
		var others []string
		for i := 1; i <= 50; i++ {
			others = append(others, fmt.Sprintf(`{"id":"o%d","name":"Other %d","owner":{"id":"u2"}}`, i, i))
		}
		fullPage := `{"items":[` + strings.Join(others, ",") + `],"total":51,"next":"NEXT"}`

		cases := []struct {
			name      string
			pages     []respStep
			wantID    string
			wantDesc  string
			wantFound bool
			wantErr   string
			wantGets  int
		}{
			{
				name: "follows next to a match",
				pages: []respStep{
					{status: 200, body: `{"items":[{"id":"o1","name":"Other","owner":{"id":"u1"}}],"next":"NEXT"}`},
					{status: 200, body: `{"items":[{"id":"pl1","name":"Last Liked","owner":{"id":"u1"},"description":"d"}],"next":null}`},
				},
				wantID: "pl1", wantDesc: "d", wantFound: true, wantGets: 2,
			},
			{
				name: "complete listing without match is not found",
				pages: []respStep{
					{status: 200, body: fullPage},
					{status: 200, body: `{"items":[{"id":"o51","name":"Other 51","owner":{"id":"u2"}}],"total":51,"next":null}`},
				},
				wantGets: 2,
			},
			{
				// The GET is retried, then the failure is surfaced instead of
				// being mistaken for "not found".
				name: "error on later page is returned",
				pages: []respStep{
					{status: 200, body: `{"items":[{"id":"o1","name":"Other","owner":{"id":"u2"}}],"total":2,"next":"NEXT"}`},
					{status: 500, body: "boom"},
				},
				wantErr: "HTTP 500: boom", wantGets: 1 + testMaxRetries,
			},
			{
				name: "incomplete listing reports every page",
				pages: []respStep{
					{status: 200, body: `{"items":[{"id":"o1","name":"Other","owner":{"id":"u2"}}],"total":2,"next":"NEXT"}`},
					{status: 200, body: `{"items":null,"total":2,"next":null}`},
				},
				wantErr:  `ruled out 1 of total 2; refusing to treat "Last Liked" as not found; API responses: [{"total":2,"next":"NEXT","items_count":1,"items":[]},{"total":2,"next":"","items_count":0,"items":null}]`,
				wantGets: 2,
			},
		}
		for _, tc := range cases {
			t.Run(tc.name, func(t *testing.T) {
				var srv *httptest.Server
				nextURL := func(page int) string { return fmt.Sprintf("%s/me/playlists?page=%d", srv.URL, page) }
				rec := &requestRecorder{}
				srv = httptest.NewServer(rec.middleware(func(w http.ResponseWriter, r *http.Request) {
					page := 1
					if p := r.URL.Query().Get("page"); p != "" {
						page, _ = strconv.Atoi(p)
					}
					st := tc.pages[page-1]
					w.WriteHeader(st.status)
					_, _ = w.Write([]byte(strings.ReplaceAll(st.body, "NEXT", nextURL(page+1))))
				}))
				defer srv.Close()

				c, _ := newTestClient(t, srv)
				id, desc, found, err := c.FindPlaylist("Last Liked", "u1")

				if tc.wantErr != "" {
					errContains(t, err, strings.ReplaceAll(tc.wantErr, "NEXT", nextURL(2)))
				} else if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
				if id != tc.wantID || desc != tc.wantDesc || found != tc.wantFound {
					t.Errorf("FindPlaylist() = (%q, %q, %v), want (%q, %q, %v)", id, desc, found, tc.wantID, tc.wantDesc, tc.wantFound)
				}
				if got := rec.count(); got != tc.wantGets {
					t.Errorf("requests = %d, want %d", got, tc.wantGets)
				}
			})
		}
	})

	t.Run("incomplete or malformed listing fails closed with diagnostics", func(t *testing.T) {
		// incomplete builds the full error text: the refusal plus the sanitized
		// per-page diagnostics (A-83).
		incomplete := func(ruledOut, total, diag string) string {
			return fmt.Sprintf(`playlist listing incomplete: ruled out %s of total %s; refusing to treat "Last Liked" as not found; API responses: %s`, ruledOut, total, diag)
		}
		cases := []struct {
			name    string
			body    string
			want    string   // substring of the error
			notWant []string // must never leak into the error
		}{
			{
				name: "empty object",
				body: `{}`,
				want: incomplete("0", "missing", `[{"total":null,"next":"","items_count":0,"items":null}]`),
			},
			{
				name: "missing total",
				body: `{"items":[{"id":"o1","name":"Other","owner":{"id":"u2"}}],"next":null}`,
				want: incomplete("1", "missing", `[{"total":null,"next":"","items_count":1,"items":[]}]`),
			},
			{
				// The production case from the A-83 screenshot.
				name: "null items with total",
				body: `{"items":null,"total":44,"next":null}`,
				want: incomplete("0", "44", `[{"total":44,"next":"","items_count":0,"items":null}]`),
			},
			{
				// All returned items ruled out but fewer than total: items reads
				// as an empty list, not as a null items field from the API.
				name: "fewer items than total",
				body: `{"items":[{"id":"o1","name":"Other","owner":{"id":"u2"}}],"total":2,"next":null}`,
				want: incomplete("1", "2", `[{"total":2,"next":"","items_count":1,"items":[]}]`),
			},
			{
				name: "null item",
				body: `{"items":[null],"total":1,"next":null}`,
				want: incomplete("0", "1", `[{"total":1,"next":"","items_count":1,"items":[null]}]`),
			},
			{
				name: "same-name ownerless item",
				body: `{"items":[{"id":"p1","name":"Last Liked"}],"total":1,"next":null}`,
				want: incomplete("0", "1", `[{"total":1,"next":"","items_count":1,"items":[{"id":"p1","name":"Last Liked","owner":{"id":""}}]}]`),
			},
			{
				name: "nameless item",
				body: `{"items":[{"id":"p1","owner":{"id":"u2"}}],"total":1,"next":null}`,
				want: incomplete("0", "1", `[{"total":1,"next":"","items_count":1,"items":[{"id":"p1","name":"","owner":{"id":"u2"}}]}]`),
			},
			{
				// Only id, name and owner.id of unruled items are reported;
				// ruled-out items and every other field are dropped.
				name: "diagnostics are sanitized",
				body: `{"items":[
					{"id":"o1","name":"Other","owner":{"id":"u2"},"description":"ruled-out-secret"},
					{"id":"p1","name":"Last Liked","owner":{},"description":"ambiguous-secret",
					 "external_urls":{"spotify":"https://open.spotify.com/playlist/p1"},
					 "images":[{"url":"https://i.scdn.co/image/cover.png"}]}
				],"total":2,"next":null}`,
				want:    incomplete("1", "2", `[{"total":2,"next":"","items_count":2,"items":[{"id":"p1","name":"Last Liked","owner":{"id":""}}]}]`),
				notWant: []string{"secret", "spotify.com", "scdn.co"},
			},
			{
				// Valid JSON of the wrong shape fails decoding: the error names
				// the mismatch but never echoes the payload.
				name:    "non-array items rejected without payload",
				body:    `{"items":{"leak":"secret-payload"},"total":1,"next":null}`,
				want:    "unexpected response: json: cannot unmarshal object",
				notWant: []string{"secret-payload"},
			},
		}
		for _, tc := range cases {
			t.Run(tc.name, func(t *testing.T) {
				srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					_, _ = w.Write([]byte(tc.body))
				}))
				defer srv.Close()

				c, _ := newTestClient(t, srv)
				id, desc, found, err := c.FindPlaylist("Last Liked", "u1")

				errContains(t, err, tc.want)
				for _, nw := range tc.notWant {
					if strings.Contains(err.Error(), nw) {
						t.Errorf("error leaks %q: %v", nw, err)
					}
				}
				if id != "" || desc != "" || found {
					t.Errorf("FindPlaylist() = (%q, %q, %v), want empty and not found on error", id, desc, found)
				}
			})
		}
	})

	t.Run("listing diagnostics are capped on a rune boundary", func(t *testing.T) {
		// A huge multi-byte id on an ambiguous entry overflows the cap, and
		// the cut lands inside a rune.
		body := fmt.Sprintf(`{"items":[{"id":%q,"name":"Last Liked"}],"total":1,"next":null}`, strings.Repeat("日", 2000))
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			_, _ = w.Write([]byte(body))
		}))
		defer srv.Close()

		c, _ := newTestClient(t, srv)
		_, _, _, err := c.FindPlaylist("Last Liked", "u1")

		errContains(t, err, "playlist listing incomplete")
		_, diag, ok := strings.Cut(err.Error(), "; API responses: ")
		if !ok {
			t.Fatalf("error has no diagnostics block: %.200q", err)
		}
		if len(diag) > 3900 {
			t.Errorf("diagnostics length = %d, want <= 3900", len(diag))
		}
		if !strings.HasSuffix(diag, "...[truncated]") {
			t.Errorf("diagnostics tail = %q, want ...[truncated] marker", diag[max(0, len(diag)-40):])
		}
		if !utf8.ValidString(diag) {
			t.Errorf("diagnostics are not valid UTF-8 after truncation")
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

func numberedTracks(n int) []helpers.Track {
	tracks := make([]helpers.Track, n)
	for i := range tracks {
		tracks[i] = helpers.Track{URI: fmt.Sprintf("uri-%03d", i)}
	}
	return tracks
}

func TestReplaceTracks(t *testing.T) {
	t.Run("empty tracks makes no request", func(t *testing.T) {
		rec := &requestRecorder{}
		mux := http.NewServeMux()
		mux.HandleFunc("/playlists/pl1/tracks", rec.middleware(serveSteps([]respStep{
			{status: 200, body: `{"snapshot_id":"snap"}`},
		})))
		srv := httptest.NewServer(mux)
		defer srv.Close()

		c, _ := newTestClient(t, srv)

		if err := c.ReplaceTracks("pl1", []helpers.Track{}); err != nil {
			t.Fatalf("unexpected error for empty tracks: %v", err)
		}
		if rec.count() != 0 {
			t.Fatalf("expected zero requests for empty tracks, got %d", rec.count())
		}
	})

	t.Run("exactly 100 tracks uses a single PUT and no POST", func(t *testing.T) {
		tracks := numberedTracks(100)
		rec := &requestRecorder{}
		mux := http.NewServeMux()
		mux.HandleFunc("/playlists/pl1/tracks", rec.middleware(serveSteps([]respStep{
			{status: 200, body: `{"snapshot_id":"snap"}`},
		})))
		srv := httptest.NewServer(mux)
		defer srv.Close()

		c, _ := newTestClient(t, srv)

		if err := c.ReplaceTracks("pl1", tracks); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		reqs := rec.all()
		if len(reqs) != 1 {
			t.Fatalf("expected exactly 1 request for 100 tracks, got %d", len(reqs))
		}
		if reqs[0].Method != http.MethodPut {
			t.Fatalf("expected PUT, got %s", reqs[0].Method)
		}
	})

	t.Run("more than 100 tracks chunks with PUT then POSTs, preserving order", func(t *testing.T) {
		total := 250
		tracks := numberedTracks(total)
		rec := &requestRecorder{}
		mux := http.NewServeMux()
		mux.HandleFunc("/playlists/pl1/tracks", rec.middleware(serveSteps([]respStep{
			{status: 200, body: `{"snapshot_id":"snap"}`},
		})))
		srv := httptest.NewServer(mux)
		defer srv.Close()

		c, _ := newTestClient(t, srv)

		if err := c.ReplaceTracks("pl1", tracks); err != nil {
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
		for i, tr := range tracks {
			if got[i] != tr.URI {
				t.Fatalf("uri[%d]: expected %q, got %q (order/completeness broken)", i, tr.URI, got[i])
			}
		}
	})

	t.Run("failed append POST returns error without retry", func(t *testing.T) {
		rec := &requestRecorder{}
		mux := http.NewServeMux()
		mux.HandleFunc("/playlists/pl1/tracks", rec.middleware(func(w http.ResponseWriter, r *http.Request) {
			if r.Method == http.MethodPost {
				w.WriteHeader(http.StatusInternalServerError)
				return
			}
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"snapshot_id":"snap"}`))
		}))
		srv := httptest.NewServer(mux)
		defer srv.Close()

		c, sleeps := newTestClient(t, srv)

		err := c.ReplaceTracks("pl1", numberedTracks(150))
		errContains(t, err, "HTTP 500")
		reqs := rec.all()
		if len(reqs) != 2 || reqs[0].Method != http.MethodPut || reqs[1].Method != http.MethodPost {
			t.Fatalf("expected exactly PUT then one POST, got %+v", reqs)
		}
		if len(sleeps.durations()) != 0 {
			t.Fatalf("expected no retry sleeps, got %v", sleeps.durations())
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
				tracks := numberedTracks(tc.total)
				rec := &requestRecorder{}
				mux := http.NewServeMux()
				mux.HandleFunc("/playlists/pl1/tracks", rec.middleware(serveSteps([]respStep{
					{status: 200, body: `{"snapshot_id":"snap"}`},
				})))
				srv := httptest.NewServer(mux)
				defer srv.Close()

				c, _ := newTestClient(t, srv)

				if err := c.ReplaceTracks("pl1", tracks); err != nil {
					t.Fatalf("ReplaceTracks(%d tracks) unexpected error: %v", tc.total, err)
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
				for i, tr := range tracks {
					if got[i] != tr.URI {
						t.Fatalf("uri[%d]: expected %q, got %q", i, tr.URI, got[i])
					}
				}
			})
		}
	})
}
