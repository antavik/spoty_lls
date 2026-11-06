package main

import (
	"io"
	"os"
	"strings"
	"testing"

	"github.com/go-pkgz/lgr"

	"spoty_lls/internal/config"
)

// clearSpotifyEnv sets all config-related env vars to empty for a clean slate,
// mirroring internal/config's clearSpotifyEnv helper. No network call should ever
// occur in these tests: both scenarios must fail before any HTTP request is made
// (config.Load fails first, or spotify.New rejects an empty refresh token before
// authenticating).
func clearSpotifyEnv(t *testing.T) {
	t.Setenv("SPOTIFY_CLIENT_ID", "")
	t.Setenv("SPOTIFY_CLIENT_SECRET", "")
	t.Setenv("SPOTIFY_REFRESH_TOKEN", "")
	t.Setenv("SPOTIFY_REDIRECT_URI", "")
	t.Setenv("DEV_MODE", "")
	t.Setenv("TELEGRAM_BOT_TOKEN", "")
	t.Setenv("TELEGRAM_CHAT_ID", "")
}

func TestRunMissingEnv(t *testing.T) {
	clearSpotifyEnv(t)
	t.Setenv("SPOTIFY_CLIENT_ID", "")
	t.Setenv("SPOTIFY_CLIENT_SECRET", "")

	got := run()

	if got != 1 {
		t.Errorf("run() = %d, want 1 when required env vars are missing (config.Load should fail)", got)
	}
}

func TestRunMissingRefreshToken(t *testing.T) {
	clearSpotifyEnv(t)
	t.Setenv("SPOTIFY_CLIENT_ID", "clientid123")
	t.Setenv("SPOTIFY_CLIENT_SECRET", "secret123")
	t.Setenv("SPOTIFY_REFRESH_TOKEN", "")

	got := run()

	if got != 1 {
		t.Errorf("run() = %d, want 1 when SPOTIFY_REFRESH_TOKEN is empty (spotify.New should reject it before any network call)", got)
	}
}

func TestSetupLogMasksSecrets(t *testing.T) {
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	orig := os.Stderr
	os.Stderr = w
	t.Cleanup(func() {
		os.Stderr = orig
		lgr.Setup()
	})

	setupLog(config.Config{ClientSecret: "csecret", RefreshToken: "rtoken", TelegramBotToken: "bottok", DevMode: true})
	lgr.Printf("[DEBUG] csecret rtoken https://api.telegram.org/botbottok/sendMessage")
	_ = w.Close()
	out, _ := io.ReadAll(r)

	for _, s := range []string{"csecret", "rtoken", "bottok"} {
		if strings.Contains(string(out), s) {
			t.Errorf("log output %q leaks secret %q", out, s)
		}
	}
	if !strings.Contains(string(out), "[DEBUG]") {
		t.Errorf("log output %q missing [DEBUG] line, want debug enabled by DevMode", out)
	}
}
