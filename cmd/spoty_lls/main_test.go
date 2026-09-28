package main

import "testing"

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
