package app

import (
	"log"
	"net/http"
	"net/url"
	"strings"

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
		log.Printf("telegram notify failed: %v", err)
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		log.Printf("telegram notify failed: HTTP %d", resp.StatusCode)
		return
	}
	config.Debugf("telegram notify sent")
}
