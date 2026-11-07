package app

import (
	"net/http"
	"net/url"
	"strings"

	"github.com/go-pkgz/lgr"

	"spoty_lls/internal/config"
)

func NotifyTelegram(botToken, chatID, text string) {
	if botToken == "" || chatID == "" {
		return
	}

	form := url.Values{
		"chat_id":                  {chatID},
		"text":                     {text},
		"disable_web_page_preview": {"true"},
	}

	client := &http.Client{Timeout: config.TelegramTimeout}
	resp, err := client.Post(
		"https://api.telegram.org/bot"+botToken+"/sendMessage",
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
