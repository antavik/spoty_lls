import os

from helpers import str2bool

# Spotify API endpoints
API_BASE = "https://api.spotify.com/v1"
TOKEN_URL = "https://accounts.spotify.com/api/token"
AUTHORIZE_URL = "https://accounts.spotify.com/authorize"

# Sync tuning / Spotify API limits
LIKED_LIMIT = 100  # max tracks to sync
PAGE = 50  # Spotify max page size for /me/tracks
BATCH = 100  # Spotify max items per add/remove request

# HTTP behaviour
MAX_RETRIES = 3
RETRY_DELAY = 2  # seconds between retries on 5xx / network errors
MAX_RETRY_WAIT = 300  # max seconds to wait on 429 (capped Retry-After)
REQUEST_TIMEOUT = 15  # seconds per HTTP request

# OAuth scopes required by this application
SCOPES = (
    "user-library-read "
    "playlist-read-private "
    "playlist-modify-private "
    "playlist-modify-public"
)

# Other
PLAYLIST_NAME = "Last Liked"
DEFAULT_REDIRECT_URI = "http://127.0.0.1:8888/callback"

# Environment-derived settings (read at import time for CLI usage)
DEV_MODE = str2bool(os.getenv("DEV_MODE", "0"))
CLIENT_ID = os.environ["SPOTIFY_CLIENT_ID"]
CLIENT_SECRET = os.environ["SPOTIFY_CLIENT_SECRET"]
REFRESH_TOKEN = os.getenv("SPOTIFY_REFRESH_TOKEN")
REDIRECT_URI = os.getenv("SPOTIFY_REDIRECT_URI", DEFAULT_REDIRECT_URI)
TELEGRAM_BOT_TOKEN = os.getenv("TELEGRAM_BOT_TOKEN")
TELEGRAM_CHAT_ID = os.getenv("TELEGRAM_CHAT_ID")
