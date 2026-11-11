package app

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"regexp"
	"strings"
	"testing"
	"unicode/utf8"
)

// partialEntityAtEnd matches a payload that ends in an '&' that is not the
// start of a complete entity: '&' followed by zero or more entity-body
// characters and no closing ';'. Every '&' in a formatted message must belong
// to a well-formed entity (a literal '&' in the input is escaped to &amp;), so
// this only happens when truncation split an entity.
var partialEntityAtEnd = regexp.MustCompile(`&[A-Za-z0-9#]*$`)

// contentOf strips the <pre> wrapper and the truncation marker from a
// formatted Telegram message, leaving the escaped payload.
func contentOf(out string) string {
	c := strings.TrimPrefix(out, "<pre>")
	c = strings.TrimSuffix(c, "</pre>")
	c = strings.TrimSuffix(c, "\n...[truncated]")
	return c
}

// tail returns the last n bytes of s, for error display only (may split a rune).
func tail(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[len(s)-n:]
}

// head returns the first n bytes of s, for error display only (may split a rune).
func head(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n]
}

// TestNotifyTelegramSendsHTMLForm pins the outbound Telegram request
// contract: POST to /bot<token>/sendMessage against the configurable base
// URL, with the message wrapped in a <pre> HTML code block, HTML-escaped,
// parse_mode=HTML, and the pre-existing form fields unchanged.
func TestNotifyTelegramSendsHTMLForm(t *testing.T) {
	var gotMethod, gotPath, gotContentType string
	var gotForm url.Values

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod = r.Method
		gotPath = r.URL.Path
		gotContentType = r.Header.Get("Content-Type")
		if err := r.ParseForm(); err != nil {
			t.Errorf("ParseForm: %v", err)
			return
		}
		gotForm = r.PostForm
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(srv.Close)

	prevBase := telegramAPIBase
	telegramAPIBase = srv.URL
	t.Cleanup(func() { telegramAPIBase = prevBase })

	const (
		token = "test-token"
		chat  = "424242"
		in    = "failed: 401 <bad> & retry"
	)
	NotifyTelegram(token, chat, in)

	if gotMethod != http.MethodPost {
		t.Errorf("method = %q, want POST", gotMethod)
	}
	wantPath := "/bot" + token + "/sendMessage"
	if gotPath != wantPath {
		t.Errorf("path = %q, want %q", gotPath, wantPath)
	}
	if gotContentType != "application/x-www-form-urlencoded" {
		t.Errorf("Content-Type = %q, want %q", gotContentType, "application/x-www-form-urlencoded")
	}
	if got := gotForm.Get("chat_id"); got != chat {
		t.Errorf("chat_id = %q, want %q", got, chat)
	}
	if got := gotForm.Get("disable_web_page_preview"); got != "true" {
		t.Errorf("disable_web_page_preview = %q, want %q", got, "true")
	}
	if got := gotForm.Get("parse_mode"); got != "HTML" {
		t.Errorf("parse_mode = %q, want %q", got, "HTML")
	}
	wantText := "<pre>failed: 401 &lt;bad&gt; &amp; retry</pre>"
	if got := gotForm.Get("text"); got != wantText {
		t.Errorf("text = %q, want %q", got, wantText)
	}
	if n := len(gotForm); n != 4 {
		t.Errorf("form has %d fields (%q), want exactly chat_id, text, parse_mode, disable_web_page_preview", n, gotForm)
	}
}

// TestFormatTelegramText pins the 3900-byte cap and the truncation shape. The
// oversized inputs also guard against infinite loops: an implementation that
// re-escapes at the same boundary forever never terminates, so these calls
// completing is itself the check.
func TestFormatTelegramText(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string // exact expected output when the input fits
		head string // head that must survive truncation when the input does not fit
	}{
		{
			name: "short_input_not_truncated",
			in:   "failed: 401 <bad> & retry",
			want: "<pre>failed: 401 &lt;bad&gt; &amp; retry</pre>",
		},
		{
			name: "escape_heavy_worst_case",
			in:   strings.Repeat("&", 5000), // 25000 bytes once escaped
			head: "<pre>&amp;",
		},
		{
			name: "oversized_unicode",
			in:   strings.Repeat("日本語のテキスト", 1000), // 7000 bytes of multi-byte runes
		},
		{
			name: "mixed_entities_and_runes",
			in:   strings.Repeat("& < 日本語 > ", 500), // ~10000 bytes once escaped
			head: "<pre>&amp;",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			out := formatTelegramText(tc.in)

			if len(out) > 3900 {
				t.Fatalf("len(out) = %d, want <= 3900", len(out))
			}
			if !utf8.ValidString(out) {
				t.Errorf("output is not valid UTF-8 (tail: %q)", tail(out, 48))
			}
			if !strings.HasPrefix(out, "<pre>") {
				t.Errorf("output = %q..., want it to start with <pre>", head(out, 48))
			}
			if !strings.HasSuffix(out, "</pre>") {
				t.Errorf("output tail = %q, want it to end with </pre>", tail(out, 48))
			}

			if tc.want != "" {
				if out != tc.want {
					t.Fatalf("out = %q, want exact %q", out, tc.want)
				}
				if strings.Contains(out, "...[truncated]") {
					t.Errorf("short input must not be truncated: %q", out)
				}
				return
			}

			if !strings.HasSuffix(out, "\n...[truncated]</pre>") {
				t.Fatalf("out tail = %q, want marker \\n...[truncated] immediately before </pre>", tail(out, 48))
			}
			if tc.head != "" && !strings.HasPrefix(out, tc.head) {
				t.Errorf("out head = %q..., want it to start with %q (message head must survive truncation)", head(out, 48), tc.head)
			}
			if len(contentOf(out)) == 0 {
				t.Errorf("truncated message kept no payload at all: %q", out)
			}
			if partialEntityAtEnd.MatchString(contentOf(out)) {
				t.Errorf("payload ends with a partial HTML entity: %q", tail(contentOf(out), 32))
			}
		})
	}
}
