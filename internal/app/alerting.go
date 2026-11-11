package app

import (
	"html"
	"net/http"
	"net/url"
	"strings"

	"github.com/go-pkgz/lgr"

	"spoty_lls/internal/config"
)

// telegramAPIBase is the Telegram Bot API base URL, kept as a variable so
// tests can point it at a local test server.
var telegramAPIBase = "https://api.telegram.org"

// The complete formatted Telegram text (wrapper + escaped payload + marker)
// is capped at telegramTextLimit bytes, safely under Telegram's 4096-char
// limit even after form encoding.
const telegramTextLimit = 3900

const (
	telegramPre       = "<pre>"
	telegramPost      = "</pre>"
	telegramTruncated = "\n...[truncated]"
)

func NotifyTelegram(botToken, chatID, text string) {
	if botToken == "" || chatID == "" {
		return
	}

	form := url.Values{
		"chat_id":                  {chatID},
		"text":                     {formatTelegramText(text)},
		"parse_mode":               {"HTML"},
		"disable_web_page_preview": {"true"},
	}

	client := &http.Client{Timeout: config.TelegramTimeout}
	resp, err := client.Post(
		telegramAPIBase+"/bot"+botToken+"/sendMessage",
		"application/x-www-form-urlencoded",
		strings.NewReader(form.Encode()),
	)
	if err != nil {
		lgr.Printf("[WARN] telegram notify failed: %v", err)
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		lgr.Printf("[WARN] telegram notify failed: HTTP %d", resp.StatusCode)
		return
	}
	lgr.Printf("[DEBUG] telegram notify sent")
}

// formatTelegramText wraps text in an HTML <pre> code block, escaping it so
// arbitrary error text (including raw Spotify response bodies) renders as
// plain text. The complete formatted string is capped at telegramTextLimit
// bytes: when needed the RAW text is shortened on rune boundaries and
// telegramTruncated is added before </pre>. Truncating before escaping keeps
// the output valid UTF-8 with no partial HTML entities.
func formatTelegramText(text string) string {
	if full := telegramPre + html.EscapeString(text) + telegramPost; len(full) <= telegramTextLimit {
		return full
	}

	// The escaped form overflows the cap. Shrink the raw text on rune
	// boundaries and re-escape. Dropping one rune frees at least 1 and at
	// most 5 bytes of escaped payload (worst case "&" -> "&amp;"), so a
	// step of overflow/5+1 always makes the candidate strictly smaller and
	// the loop terminates: a one-rune candidate is at most 5 + 5 + 15 + 6 =
	// 31 bytes, well under the cap, so at least the first rune always
	// survives.
	runes := []rune(text)
	n := len(runes)
	for {
		candidate := telegramPre + html.EscapeString(string(runes[:n])) + telegramTruncated + telegramPost
		if len(candidate) <= telegramTextLimit {
			return candidate
		}
		step := (len(candidate)-telegramTextLimit)/5 + 1
		n -= step
		if n < 1 {
			n = 1
		}
	}
}
