package config

import (
	"fmt"
	"log"
	"os"
	"strconv"
	"time"

	"spoty_lls/internal/helpers"
)

const (
	APIBase      = "https://api.spotify.com/v1"
	TokenURL     = "https://accounts.spotify.com/api/token"
	AuthorizeURL = "https://accounts.spotify.com/authorize"

	Scopes = "user-library-read playlist-read-private playlist-modify-private playlist-modify-public"

	DefaultPlaylistName = "Last Liked"
	DefaultRedirectURI  = "http://127.0.0.1:8888/callback"

	DefaultLikedLimit = 100
	Page              = 50
	Batch             = 100
	MaxRetries        = 3

	RetryDelay      = 2 * time.Second
	MaxRetryWait    = 300 * time.Second
	RequestTimeout  = 15 * time.Second
	TelegramTimeout = 10 * time.Second
)

var DebugMode bool

func Debugf(format string, args ...any) {
	if DebugMode {
		log.Printf(format, args...)
	}
}

type Config struct {
	ClientID         string
	ClientSecret     string
	RefreshToken     string
	RedirectURI      string
	PlaylistName     string
	DevMode          bool
	LikedLimit       int
	TelegramBotToken string
	TelegramChatID   string
}

func Load() (Config, error) {
	clientID := os.Getenv("SPOTIFY_CLIENT_ID")
	if clientID == "" {
		return Config{}, fmt.Errorf("SPOTIFY_CLIENT_ID not set")
	}
	clientSecret := os.Getenv("SPOTIFY_CLIENT_SECRET")
	if clientSecret == "" {
		return Config{}, fmt.Errorf("SPOTIFY_CLIENT_SECRET not set")
	}

	devModeRaw := os.Getenv("DEV_MODE")
	if devModeRaw == "" {
		devModeRaw = "0"
	}
	devMode, err := helpers.Str2Bool(devModeRaw)
	if err != nil {
		return Config{}, fmt.Errorf("invalid DEV_MODE: %w", err)
	}

	likedLimit := DefaultLikedLimit
	if raw := os.Getenv("SPOTIFY_LIKED_LIMIT"); raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil {
			return Config{}, fmt.Errorf("invalid SPOTIFY_LIKED_LIMIT: %w", err)
		}
		if n <= 0 {
			return Config{}, fmt.Errorf("invalid SPOTIFY_LIKED_LIMIT: %d must be a positive integer", n)
		}
		likedLimit = n
	}

	redirectURI := os.Getenv("SPOTIFY_REDIRECT_URI")
	if redirectURI == "" {
		redirectURI = DefaultRedirectURI
	}

	playlistName := DefaultPlaylistName
	if v := os.Getenv("SPOTIFY_PLAYLIST_NAME"); v != "" {
		playlistName = v
	}

	return Config{
		ClientID:         clientID,
		ClientSecret:     clientSecret,
		RefreshToken:     os.Getenv("SPOTIFY_REFRESH_TOKEN"),
		RedirectURI:      redirectURI,
		PlaylistName:     playlistName,
		DevMode:          devMode,
		LikedLimit:       likedLimit,
		TelegramBotToken: os.Getenv("TELEGRAM_BOT_TOKEN"),
		TelegramChatID:   os.Getenv("TELEGRAM_CHAT_ID"),
	}, nil
}
