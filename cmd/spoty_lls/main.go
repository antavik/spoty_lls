package main

import (
	"os"

	"github.com/go-pkgz/lgr"

	"spoty_lls/internal/app"
	"spoty_lls/internal/config"
	"spoty_lls/internal/spotify"
)

func main() {
	os.Exit(run())
}

func run() int {
	cfg, err := config.Load()
	setupLog(cfg) // zero cfg on error: info level, nothing to mask
	if err != nil {
		lgr.Printf("[ERROR] spoty_lls failed: %v", err)
		return 1
	}

	client, err := spotify.New(cfg.ClientID, cfg.ClientSecret, cfg.RefreshToken)
	if err != nil {
		msg := "spoty_lls failed: " + err.Error()
		app.NotifyTelegram(cfg.TelegramBotToken, cfg.TelegramChatID, msg)
		lgr.Printf("[ERROR] %s", msg)
		return 1
	}

	notify := func(text string) {
		app.NotifyTelegram(cfg.TelegramBotToken, cfg.TelegramChatID, text)
	}
	if err := app.Run(cfg, client, notify); err != nil {
		return 1
	}
	return 0
}

func setupLog(cfg config.Config) {
	opts := []lgr.Option{lgr.Out(os.Stderr), lgr.Err(os.Stderr), lgr.LevelBraces}
	if cfg.DevMode {
		opts = append(opts, lgr.Debug, lgr.CallerFile)
	}
	lgr.Setup(opts...)
	lgr.AddSecrets(cfg.ClientSecret, cfg.RefreshToken, cfg.TelegramBotToken)
}
