package app

import (
	"html"
	"io"
	"net/http"
	"net/url"
	"strings"

	"github.com/go-pkgz/lgr"

	"spoty_lls/internal/config"
)

var telegramAPIBase = "https://api.telegram.org"
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
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
		lgr.Printf("[WARN] telegram notify failed: HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
		return
	}
	lgr.Printf("[DEBUG] telegram notify sent")
}

// formatTelegramText wraps text in an HTML <pre> code block, escaping it so
// arbitrary error text (including raw Spotify response bodies) renders as
// plain text. Invalid UTF-8 is replaced first, since Telegram rejects it. The
// complete formatted string is capped at telegramTextLimit bytes: when needed
// the text is cut on a rune boundary (escaping rune by rune, so no partial
// HTML entity) and telegramTruncated is added before </pre>.
func formatTelegramText(text string) string {
	text = strings.ToValidUTF8(text, "\uFFFD")
	escaped := html.EscapeString(text)
	if len(telegramPre)+len(escaped)+len(telegramPost) <= telegramTextLimit {
		return telegramPre + escaped + telegramPost
	}

	budget := telegramTextLimit - len(telegramPre) - len(telegramTruncated) - len(telegramPost)
	var b strings.Builder
	b.WriteString(telegramPre)
	for _, r := range text {
		e := html.EscapeString(string(r))
		if budget -= len(e); budget < 0 {
			break
		}
		b.WriteString(e)
	}
	b.WriteString(telegramTruncated + telegramPost)
	return b.String()
}
