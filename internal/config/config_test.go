package config

import (
	"os"
	"strings"
	"testing"
	"time"
)

// clearSpotifyEnv sets required-related env vars to empty/unset defaults for a clean slate.
func clearSpotifyEnv(t *testing.T) {
	t.Setenv("SPOTIFY_CLIENT_ID", "")
	t.Setenv("SPOTIFY_CLIENT_SECRET", "")
	t.Setenv("SPOTIFY_REFRESH_TOKEN", "")
	t.Setenv("SPOTIFY_REDIRECT_URI", "")
	t.Setenv("DEV_MODE", "")
	t.Setenv("TELEGRAM_BOT_TOKEN", "")
	t.Setenv("TELEGRAM_CHAT_ID", "")
	t.Setenv("SPOTIFY_LIKED_LIMIT", "")
	t.Setenv("SPOTIFY_PLAYLIST_NAME", "")
}

func TestLoadRequired(t *testing.T) {
	t.Run("missing client id errors", func(t *testing.T) {
		clearSpotifyEnv(t)
		t.Setenv("SPOTIFY_CLIENT_ID", "")
		t.Setenv("SPOTIFY_CLIENT_SECRET", "secret123")

		_, err := Load()
		if err == nil {
			t.Errorf("Load() with missing SPOTIFY_CLIENT_ID returned nil error, want non-nil")
		}
	})

	t.Run("missing client secret errors", func(t *testing.T) {
		clearSpotifyEnv(t)
		t.Setenv("SPOTIFY_CLIENT_ID", "clientid123")
		t.Setenv("SPOTIFY_CLIENT_SECRET", "")

		_, err := Load()
		if err == nil {
			t.Errorf("Load() with missing SPOTIFY_CLIENT_SECRET returned nil error, want non-nil")
		}
	})

	t.Run("both set with refresh token succeeds", func(t *testing.T) {
		clearSpotifyEnv(t)
		t.Setenv("SPOTIFY_CLIENT_ID", "clientid123")
		t.Setenv("SPOTIFY_CLIENT_SECRET", "secret123")
		t.Setenv("SPOTIFY_REFRESH_TOKEN", "refreshtok123")

		cfg, err := Load()
		if err != nil {
			t.Fatalf("Load() returned unexpected error: %v", err)
		}
		if cfg.ClientID != "clientid123" {
			t.Errorf("cfg.ClientID = %q, want %q", cfg.ClientID, "clientid123")
		}
		if cfg.ClientSecret != "secret123" {
			t.Errorf("cfg.ClientSecret = %q, want %q", cfg.ClientSecret, "secret123")
		}
		if cfg.RefreshToken != "refreshtok123" {
			t.Errorf("cfg.RefreshToken = %q, want %q", cfg.RefreshToken, "refreshtok123")
		}
	})
}

func TestLoadDefaults(t *testing.T) {
	clearSpotifyEnv(t)
	t.Setenv("SPOTIFY_CLIENT_ID", "clientid123")
	t.Setenv("SPOTIFY_CLIENT_SECRET", "secret123")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() returned unexpected error: %v", err)
	}
	if cfg.RedirectURI != DefaultRedirectURI {
		t.Errorf("cfg.RedirectURI = %q, want %q (DefaultRedirectURI)", cfg.RedirectURI, DefaultRedirectURI)
	}
	if cfg.DevMode != false {
		t.Errorf("cfg.DevMode = %v, want false", cfg.DevMode)
	}
	if cfg.TelegramBotToken != "" {
		t.Errorf("cfg.TelegramBotToken = %q, want empty", cfg.TelegramBotToken)
	}
	if cfg.TelegramChatID != "" {
		t.Errorf("cfg.TelegramChatID = %q, want empty", cfg.TelegramChatID)
	}
}

func TestLoadDevMode(t *testing.T) {
	cases := []struct {
		name    string
		devMode string
		want    bool
		wantErr bool
	}{
		{"one", "1", true, false},
		{"yes", "yes", true, false},
		{"zero", "0", false, false},
		{"bogus", "bogus", false, true},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			clearSpotifyEnv(t)
			t.Setenv("SPOTIFY_CLIENT_ID", "clientid123")
			t.Setenv("SPOTIFY_CLIENT_SECRET", "secret123")
			t.Setenv("DEV_MODE", tc.devMode)

			cfg, err := Load()
			if tc.wantErr {
				if err == nil {
					t.Errorf("Load() with DEV_MODE=%q returned nil error, want non-nil", tc.devMode)
				}
				return
			}
			if err != nil {
				t.Fatalf("Load() with DEV_MODE=%q returned unexpected error: %v", tc.devMode, err)
			}
			if cfg.DevMode != tc.want {
				t.Errorf("Load() with DEV_MODE=%q -> cfg.DevMode = %v, want %v", tc.devMode, cfg.DevMode, tc.want)
			}
		})
	}
}

func TestLoadOptional(t *testing.T) {
	clearSpotifyEnv(t)
	t.Setenv("SPOTIFY_CLIENT_ID", "clientid123")
	t.Setenv("SPOTIFY_CLIENT_SECRET", "secret123")
	t.Setenv("SPOTIFY_REFRESH_TOKEN", "refreshtok123")
	t.Setenv("SPOTIFY_REDIRECT_URI", "http://example.com/callback")
	t.Setenv("TELEGRAM_BOT_TOKEN", "botTok123")
	t.Setenv("TELEGRAM_CHAT_ID", "chat123")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() returned unexpected error: %v", err)
	}
	if cfg.RefreshToken != "refreshtok123" {
		t.Errorf("cfg.RefreshToken = %q, want %q", cfg.RefreshToken, "refreshtok123")
	}
	if cfg.RedirectURI != "http://example.com/callback" {
		t.Errorf("cfg.RedirectURI = %q, want %q", cfg.RedirectURI, "http://example.com/callback")
	}
	if cfg.TelegramBotToken != "botTok123" {
		t.Errorf("cfg.TelegramBotToken = %q, want %q", cfg.TelegramBotToken, "botTok123")
	}
	if cfg.TelegramChatID != "chat123" {
		t.Errorf("cfg.TelegramChatID = %q, want %q", cfg.TelegramChatID, "chat123")
	}
}

func TestLoadLikedLimit(t *testing.T) {
	cases := []struct {
		name    string
		set     bool // whether SPOTIFY_LIKED_LIMIT is set at all (false = unset)
		value   string
		want    int
		wantErr bool
	}{
		{"unset defaults to DefaultLikedLimit", false, "", DefaultLikedLimit, false},
		{"empty defaults to DefaultLikedLimit", true, "", DefaultLikedLimit, false},
		{"valid positive integer", true, "50", 50, false},
		{"non-numeric errors", true, "abc", 0, true},
		{"zero errors", true, "0", 0, true},
		{"negative errors", true, "-5", 0, true},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			clearSpotifyEnv(t)
			t.Setenv("SPOTIFY_CLIENT_ID", "clientid123")
			t.Setenv("SPOTIFY_CLIENT_SECRET", "secret123")
			if tc.set {
				t.Setenv("SPOTIFY_LIKED_LIMIT", tc.value)
			} else {
				// t.Setenv in clearSpotifyEnv registered restore-on-cleanup;
				// unset for this subtest only.
				if err := os.Unsetenv("SPOTIFY_LIKED_LIMIT"); err != nil {
					t.Fatalf("os.Unsetenv(SPOTIFY_LIKED_LIMIT): %v", err)
				}
			}

			cfg, err := Load()
			if tc.wantErr {
				if err == nil {
					t.Fatalf("Load() with SPOTIFY_LIKED_LIMIT=%q returned nil error, want non-nil", tc.value)
				}
				if !strings.Contains(err.Error(), "SPOTIFY_LIKED_LIMIT") {
					t.Errorf("Load() error = %q, want it to mention %q", err.Error(), "SPOTIFY_LIKED_LIMIT")
				}
				return
			}
			if err != nil {
				t.Fatalf("Load() with SPOTIFY_LIKED_LIMIT=%q returned unexpected error: %v", tc.value, err)
			}
			if cfg.LikedLimit != tc.want {
				t.Errorf("Load() with SPOTIFY_LIKED_LIMIT=%q -> cfg.LikedLimit = %d, want %d", tc.value, cfg.LikedLimit, tc.want)
			}
		})
	}
}

func TestLoadPlaylistName(t *testing.T) {
	cases := []struct {
		name  string
		set   bool // whether SPOTIFY_PLAYLIST_NAME is set at all (false = unset)
		value string
		want  string
	}{
		{"unset defaults to DefaultPlaylistName", false, "", DefaultPlaylistName},
		{"empty defaults to DefaultPlaylistName", true, "", DefaultPlaylistName},
		{"custom name overrides default", true, "My Weekly Likes", "My Weekly Likes"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			clearSpotifyEnv(t)
			t.Setenv("SPOTIFY_CLIENT_ID", "clientid123")
			t.Setenv("SPOTIFY_CLIENT_SECRET", "secret123")
			if tc.set {
				t.Setenv("SPOTIFY_PLAYLIST_NAME", tc.value)
			} else {
				// t.Setenv in clearSpotifyEnv registered restore-on-cleanup;
				// unset for this subtest only.
				if err := os.Unsetenv("SPOTIFY_PLAYLIST_NAME"); err != nil {
					t.Fatalf("os.Unsetenv(SPOTIFY_PLAYLIST_NAME): %v", err)
				}
			}

			cfg, err := Load()
			if err != nil {
				t.Fatalf("Load() with SPOTIFY_PLAYLIST_NAME=%q returned unexpected error: %v", tc.value, err)
			}
			if cfg.PlaylistName != tc.want {
				t.Errorf("Load() with SPOTIFY_PLAYLIST_NAME=%q -> cfg.PlaylistName = %q, want %q", tc.value, cfg.PlaylistName, tc.want)
			}
		})
	}
}

func TestConstants(t *testing.T) {
	if DefaultPlaylistName != "Last Liked" {
		t.Errorf("DefaultPlaylistName = %q, want %q", DefaultPlaylistName, "Last Liked")
	}
	if DefaultLikedLimit != 100 {
		t.Errorf("DefaultLikedLimit = %d, want 100", DefaultLikedLimit)
	}
	if Page != 50 {
		t.Errorf("Page = %d, want 50", Page)
	}
	if Batch != 100 {
		t.Errorf("Batch = %d, want 100", Batch)
	}
	if MaxRetries != 3 {
		t.Errorf("MaxRetries = %d, want 3", MaxRetries)
	}
	if RetryDelay != 2*time.Second {
		t.Errorf("RetryDelay = %v, want %v", RetryDelay, 2*time.Second)
	}
	if MaxRetryWait != 300*time.Second {
		t.Errorf("MaxRetryWait = %v, want %v", MaxRetryWait, 300*time.Second)
	}
	if RequestTimeout != 15*time.Second {
		t.Errorf("RequestTimeout = %v, want %v", RequestTimeout, 15*time.Second)
	}

	scopeNames := []string{
		"user-library-read",
		"playlist-read-private",
		"playlist-modify-private",
		"playlist-modify-public",
	}
	for _, s := range scopeNames {
		if !strings.Contains(Scopes, s) {
			t.Errorf("Scopes %q missing expected scope %q", Scopes, s)
		}
	}
}

func TestDebugf(t *testing.T) {
	t.Run("disabled does not panic", func(t *testing.T) {
		DebugMode = false
		Debugf("x %d", 1)
	})

	t.Run("enabled does not panic", func(t *testing.T) {
		DebugMode = true
		Debugf("x %d", 1)
		DebugMode = false
	})
}
