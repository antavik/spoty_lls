package main

import (
	"log"
	"os"

	"spoty_lls/internal/app"
	"spoty_lls/internal/config"
	"spoty_lls/internal/spotify"
)

func main() {
	os.Exit(run())
}

func run() int {
	cfg, err := config.Load()
	if err != nil {
		log.Printf("spoty_lls failed: %v", err)
		return 1
	}
	config.DebugMode = cfg.DevMode

	client, err := spotify.New(cfg.ClientID, cfg.ClientSecret, cfg.RefreshToken)
	if err != nil {
		msg := "spoty_lls failed: " + err.Error()
		app.NotifyTelegram(cfg.TelegramBotToken, cfg.TelegramChatID, msg)
		log.Print(msg)
		return 1
	}

	notify := func(text string) {
		app.NotifyTelegram(cfg.TelegramBotToken, cfg.TelegramChatID, text)
	}
	if err := app.Run(client, notify); err != nil {
		return 1
	}
	return 0
}
