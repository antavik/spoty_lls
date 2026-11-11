package spotify

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/go-pkgz/lgr"

	"spoty_lls/internal/config"
	"spoty_lls/internal/helpers"
)

type Client struct {
	clientID     string
	clientSecret string
	refreshToken string
	token        string

	http     *http.Client
	apiBase  string
	tokenURL string
	sleep    func(time.Duration)
}

type tokenResponse struct {
	AccessToken string `json:"access_token"`
}

func New(clientID, clientSecret, refreshToken string) (*Client, error) {
	if refreshToken == "" {
		return nil, fmt.Errorf("SPOTIFY_REFRESH_TOKEN not set")
	}
	return &Client{
		clientID:     clientID,
		clientSecret: clientSecret,
		refreshToken: refreshToken,
		http:         &http.Client{Timeout: config.RequestTimeout},
		apiBase:      config.APIBase,
		tokenURL:     config.TokenURL,
		sleep:        time.Sleep,
	}, nil
}

func (c *Client) Authenticate() error {
	form := url.Values{
		"grant_type":    {"refresh_token"},
		"refresh_token": {c.refreshToken},
	}

	req, err := http.NewRequest(http.MethodPost, c.tokenURL, strings.NewReader(form.Encode()))
	if err != nil {
		return fmt.Errorf("token refresh failed: %w", err)
	}
	basic := base64.StdEncoding.EncodeToString([]byte(c.clientID + ":" + c.clientSecret))
	req.Header.Set("Authorization", "Basic "+basic)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("token refresh failed: %w", err)
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode >= 400 {
		return fmt.Errorf("token refresh failed: HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}

	var tok tokenResponse
	if err := json.Unmarshal(body, &tok); err != nil {
		return fmt.Errorf("token refresh failed: invalid JSON response")
	}
	if tok.AccessToken == "" {
		return fmt.Errorf("token refresh failed: no access_token in response")
	}

	c.token = tok.AccessToken
	return nil
}

func (c *Client) CurrentUserID() (string, error) {
	var user User
	if err := c.do(http.MethodGet, c.apiBase+"/me", nil, &user); err != nil {
		return "", err
	}
	if user.ID == "" {
		return "", fmt.Errorf("unexpected response from /me: missing 'id'")
	}
	return user.ID, nil
}

func (c *Client) LikedTracks(limit int) ([]helpers.Track, error) {
	var tracks []helpers.Track
	nextURL := fmt.Sprintf("%s/me/tracks?limit=%d", c.apiBase, config.Page)

	for nextURL != "" {
		var page Paging[SavedTrack]
		if err := c.do(http.MethodGet, nextURL, nil, &page); err != nil {
			return nil, err
		}

		for _, item := range page.Items {
			if t := item.Track; t.URI != "" {
				var names []string
				for _, a := range t.Artists {
					if a.Name != "" {
						names = append(names, a.Name)
					}
				}
				tracks = append(tracks, helpers.Track{URI: t.URI, Title: t.Name, Artists: names})
			}
			if len(tracks) >= limit {
				return tracks, nil
			}
		}

		nextURL = page.Next
	}

	return tracks, nil
}

type playlistRef struct {
	ID    string        `json:"id"`
	Name  string        `json:"name"`
	Owner PlaylistOwner `json:"owner"`
}

type listingPage struct {
	Total      *int           `json:"total"`
	Next       string         `json:"next"`
	ItemsCount int            `json:"items_count"`
	Items      []*playlistRef `json:"items"` // unruled items only; null if the API sent none
}

func (c *Client) FindPlaylist(name, ownerID string) (id, description string, found bool, err error) {
	nextURL := fmt.Sprintf("%s/me/playlists?limit=%d", c.apiBase, config.Page)

	ruledOut := 0
	var total *int
	var pages []listingPage

	for nextURL != "" {
		var page Paging[*SimplifiedPlaylist]
		if err := c.do(http.MethodGet, nextURL, nil, &page); err != nil {
			return "", "", false, err
		}

		var ambiguous []*playlistRef
		if page.Items != nil {
			ambiguous = []*playlistRef{} // an all-ruled-out page shows [], not null
		}
		for _, pl := range page.Items {
			if pl == nil {
				ambiguous = append(ambiguous, nil)
				continue
			}
			if pl.Name == name && pl.Owner.ID == ownerID {
				return pl.ID, pl.Description, true, nil
			}

			// Rule out only provably wrong matches: different names are excluded,
			// same-name entries only if the owner is known to differ; nameless
			// or ownerless same-name entries stay ambiguous.
			if pl.Name != "" && (pl.Name != name || pl.Owner.ID != "") {
				ruledOut++
				continue
			}
			ambiguous = append(ambiguous, &playlistRef{ID: pl.ID, Name: pl.Name, Owner: pl.Owner})
		}

		total = page.Total
		pages = append(pages, listingPage{Total: total, Next: page.Next, ItemsCount: len(page.Items), Items: ambiguous})

		nextURL = page.Next
	}

	// A miss must be proven: a malformed or incomplete listing can create a
	// duplicate. Require a known total fully covered by ruled-out entries
	// before treating the playlist as absent.
	if total == nil || ruledOut < *total {
		totalText := "missing"
		if total != nil {
			totalText = strconv.Itoa(*total)
		}
		// Append a sanitized, size-capped view of the listing pages that
		// triggered the refusal (marshaling these plain structs cannot fail).
		diag, _ := json.Marshal(pages)
		const diagCap, marker = 3900, "...[truncated]"
		if len(diag) > diagCap {
			diag = []byte(strings.ToValidUTF8(string(diag[:diagCap-len(marker)]), "?") + marker)
		}
		return "", "", false, fmt.Errorf("playlist listing incomplete: ruled out %d of total %s; refusing to treat %q as not found; API responses: %s", ruledOut, totalText, name, diag)
	}

	return "", "", false, nil
}

func (c *Client) CreatePlaylist(ownerID, name string) (string, error) {
	var pl SimplifiedPlaylist
	err := c.do(
		http.MethodPost,
		fmt.Sprintf("%s/users/%s/playlists", c.apiBase, ownerID),
		map[string]any{"name": name, "public": false},
		&pl,
	)
	if err != nil {
		return "", err
	}
	if pl.ID == "" {
		return "", fmt.Errorf("unexpected response from create playlist: missing 'id'")
	}
	return pl.ID, nil
}

func (c *Client) ChangeDetails(playlistID, description string) error {
	if description == "" {
		return nil
	}
	return c.do(
		http.MethodPut,
		fmt.Sprintf("%s/playlists/%s", c.apiBase, playlistID),
		map[string]any{"description": description},
		nil,
	)
}

func (c *Client) ReplaceTracks(playlistID string, tracks []helpers.Track) error {
	if len(tracks) == 0 {
		lgr.Printf("[DEBUG] replace_tracks called with empty tracks - skipping")
		return nil
	}

	tracksURL := fmt.Sprintf("%s/playlists/%s/tracks", c.apiBase, playlistID)

	var snap Snapshot
	if err := c.do(http.MethodPut, tracksURL, map[string]any{"uris": helpers.URIs(tracks)[:min(len(tracks), config.Batch)]}, &snap); err != nil {
		return err
	}
	if snap.SnapshotID != "" {
		lgr.Printf("[DEBUG] replace_tracks snapshot_id=%s", snap.SnapshotID)
	}

	for start := config.Batch; start < len(tracks); start += config.Batch {
		end := min(start+config.Batch, len(tracks))
		if err := c.do(http.MethodPost, tracksURL, map[string]any{"uris": helpers.URIs(tracks)[start:end]}, nil); err != nil {
			return err
		}
	}

	return nil
}

func (c *Client) do(method, url string, body, out any) error {
	if c.token == "" {
		return fmt.Errorf("client not authenticated")
	}

	attempt := 1
	for {
		retry, wait, err := c.attempt(method, url, body, out)
		if err == nil || !retry {
			return err
		}
		if attempt >= config.MaxRetries {
			return err
		}
		lgr.Printf("[DEBUG] %s %s failed (%v); retry %d/%d in %s", method, url, err, attempt, config.MaxRetries, wait)
		if wait > 0 {
			c.sleep(wait)
		}
		attempt++
	}
}

func (c *Client) attempt(method, url string, body, out any) (retry bool, wait time.Duration, err error) {
	var bodyReader io.Reader
	if body != nil {
		encoded, encErr := json.Marshal(body)
		if encErr != nil {
			return false, 0, fmt.Errorf("%s %s: encode body: %w", method, url, encErr)
		}
		bodyReader = strings.NewReader(string(encoded))
	}

	req, reqErr := http.NewRequest(method, url, bodyReader)
	if reqErr != nil {
		return false, 0, fmt.Errorf("%s %s: %w", method, url, reqErr)
	}
	req.Header.Set("Authorization", "Bearer "+c.token)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	// A POST (CreatePlaylist, ReplaceTracks append) may already have been
	// applied server-side when a network error or 5xx happens, so its
	// outcome is unknown: retrying it could duplicate the playlist/tracks.
	retryUnknown := method != http.MethodPost

	resp, doErr := c.http.Do(req)
	if doErr != nil {
		return retryUnknown, config.RetryDelay, fmt.Errorf("%s %s: network error: %w", method, url, doErr)
	}
	defer resp.Body.Close()

	content, _ := io.ReadAll(resp.Body)

	switch {
	case resp.StatusCode == http.StatusUnauthorized:
		if authErr := c.Authenticate(); authErr != nil {
			return false, 0, authErr
		}
		return true, 0,
			fmt.Errorf("%s %s -> HTTP 401: retrying after token refresh: %s", method, url, strings.TrimSpace(string(content)))

	case resp.StatusCode == http.StatusTooManyRequests:
		return true, c.retryAfter(resp),
			fmt.Errorf("%s %s -> HTTP 429: %s", method, url, strings.TrimSpace(string(content)))

	case resp.StatusCode >= 500:
		return retryUnknown, config.RetryDelay,
			fmt.Errorf("%s %s -> HTTP %d: %s", method, url, resp.StatusCode, strings.TrimSpace(string(content)))

	case resp.StatusCode >= 400:
		return false, 0,
			fmt.Errorf("%s %s -> HTTP %d: %s", method, url, resp.StatusCode, strings.TrimSpace(string(content)))
	}

	if out == nil || len(content) == 0 {
		return false, 0, nil
	}
	if jsonErr := json.Unmarshal(content, out); jsonErr != nil {
		var typeErr *json.UnmarshalTypeError
		if errors.As(jsonErr, &typeErr) {
			return false, 0, fmt.Errorf("%s %s -> unexpected response: %w", method, url, jsonErr)
		}
		return false, 0,
			fmt.Errorf("%s %s -> invalid JSON response: %s", method, url, strings.TrimSpace(string(content)))
	}
	return false, 0, nil
}

func (c *Client) retryAfter(resp *http.Response) time.Duration {
	wait := config.RetryDelay
	if header := resp.Header.Get("Retry-After"); header != "" {
		if seconds, err := strconv.Atoi(header); err == nil {
			wait = time.Duration(seconds) * time.Second
		}
	}
	return min(wait, config.MaxRetryWait)
}
