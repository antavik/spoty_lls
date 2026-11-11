package app

import (
	"bytes"
	"html"
	"net/http"
	"net/http/httptest"
	"net/url"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"unicode/utf8"

	"github.com/go-pkgz/lgr"
)

// TestNotifyTelegram pins the outbound Telegram request contract (POST
// /bot<token>/sendMessage with an HTML-escaped <pre> message and
// parse_mode=HTML), the no-op guard for missing credentials, and logging of
// Telegram's rejection body.
func TestNotifyTelegram(t *testing.T) {
	const reply = `{"ok":false,"description":"Bad Request: can't parse entities"}`
	wantForm := url.Values{
		"chat_id":                  {"424242"},
		"text":                     {"<pre>failed: 401 &lt;bad&gt; &amp; retry</pre>"},
		"parse_mode":               {"HTML"},
		"disable_web_page_preview": {"true"},
	}

	cases := []struct {
		name         string
		token, chat  string
		status       int
		reply        string
		wantRequests int
		wantLog      string // substring; "" skips the check
	}{
		{name: "sends HTML form", token: "test-token", chat: "424242", status: http.StatusOK, wantRequests: 1},
		{name: "logs rejection body", token: "test-token", chat: "424242", status: http.StatusBadRequest, reply: reply,
			wantRequests: 1, wantLog: "WARN  telegram notify failed: HTTP 400: " + reply},
		{name: "missing bot token is a no-op", chat: "424242", status: http.StatusOK},
		{name: "missing chat id is a no-op", token: "test-token", status: http.StatusOK},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var requests atomic.Int32
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				requests.Add(1)
				if r.Method != http.MethodPost || r.URL.Path != "/bottest-token/sendMessage" {
					t.Errorf("request = %s %s, want POST /bottest-token/sendMessage", r.Method, r.URL.Path)
				}
				if ct := r.Header.Get("Content-Type"); ct != "application/x-www-form-urlencoded" {
					t.Errorf("Content-Type = %q, want application/x-www-form-urlencoded", ct)
				}
				if err := r.ParseForm(); err != nil {
					t.Errorf("ParseForm: %v", err)
				}
				if !reflect.DeepEqual(r.PostForm, wantForm) {
					t.Errorf("form = %q, want %q", r.PostForm, wantForm)
				}
				w.WriteHeader(tc.status)
				_, _ = w.Write([]byte(tc.reply))
			}))
			t.Cleanup(srv.Close)

			prevBase := telegramAPIBase
			telegramAPIBase = srv.URL
			t.Cleanup(func() { telegramAPIBase = prevBase })

			var buf bytes.Buffer
			lgr.Setup(lgr.Out(&buf), lgr.Err(&buf))
			t.Cleanup(func() { lgr.Setup() })

			NotifyTelegram(tc.token, tc.chat, "failed: 401 <bad> & retry")

			if got := int(requests.Load()); got != tc.wantRequests {
				t.Errorf("requests = %d, want %d", got, tc.wantRequests)
			}
			if tc.wantLog != "" && !strings.Contains(buf.String(), tc.wantLog) {
				t.Errorf("log = %q, want it to contain %q", buf.String(), tc.wantLog)
			}
		})
	}
}

// TestFormatTelegramText pins the 3900-byte cap and the truncation shape. The
// oversized inputs also guard against infinite loops: an implementation that
// re-escapes at the same boundary forever never terminates, so these calls
// completing is itself the check.
func TestFormatTelegramText(t *testing.T) {
	const (
		limit  = 3900
		wrap   = len("<pre>") + len("</pre>")
		marker = "\n...[truncated]</pre>"
	)
	fits := strings.Repeat("a", limit-wrap)

	cases := []struct {
		name string
		in   string
		want string // exact output; "" means the input must be truncated
	}{
		{name: "short input escaped", in: "failed: 401 <bad> & retry", want: "<pre>failed: 401 &lt;bad&gt; &amp; retry</pre>"},
		{name: "invalid UTF-8 replaced", in: "bad \xff\xfe body", want: "<pre>bad � body</pre>"},
		{name: "exactly at limit not truncated", in: fits, want: "<pre>" + fits + "</pre>"},
		{name: "one byte over limit", in: fits + "a"},
		{name: "escaping pushes over limit", in: fits[4:] + "&"},
		{name: "escape-heavy worst case", in: strings.Repeat("&", 5000)},
		{name: "oversized multi-byte runes", in: strings.Repeat("日本語のテキスト", 1000)},
		{name: "oversized invalid UTF-8", in: strings.Repeat("a\xff", 3000)},
		{name: "mixed entities and runes", in: strings.Repeat("& < 日本語 > ", 500)},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			out := formatTelegramText(tc.in)

			if len(out) > limit {
				t.Fatalf("len(out) = %d, want <= %d", len(out), limit)
			}
			if !utf8.ValidString(out) {
				t.Errorf("output is not valid UTF-8")
			}
			if tc.want != "" {
				if out != tc.want {
					t.Errorf("out = %q, want %q", out, tc.want)
				}
				return
			}

			if !strings.HasPrefix(out, "<pre>") || !strings.HasSuffix(out, marker) {
				t.Fatalf("out = %q...%q, want <pre>...%q", out[:min(len(out), 16)], out[max(0, len(out)-32):], marker)
			}
			// The kept payload must be the escaped form of a prefix of the
			// input: this rejects split entities and lost message heads.
			payload := strings.TrimSuffix(strings.TrimPrefix(out, "<pre>"), marker)
			raw := html.UnescapeString(payload)
			if html.EscapeString(raw) != payload || !strings.HasPrefix(strings.ToValidUTF8(tc.in, "�"), raw) {
				t.Errorf("payload is not an escaped input prefix (tail %q)", payload[max(0, len(payload)-32):])
			}
			// The budget is used up to less than one escaped rune (max 5 bytes).
			if len(out) < limit-5 {
				t.Errorf("len(out) = %d, want >= %d (truncation wastes budget)", len(out), limit-5)
			}
		})
	}
}
