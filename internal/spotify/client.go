package spotify

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"spoty_lls/internal/config"
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
	result, err := c.do(http.MethodGet, c.apiBase+"/me", nil)
	if err != nil {
		return "", err
	}
	userID, _ := result["id"].(string)
	if userID == "" {
		return "", fmt.Errorf("unexpected response from /me: missing 'id'")
	}
	return userID, nil
}

func (c *Client) LikedTrackURIs(limit int) ([]string, error) {
	var uris []string
	nextURL := fmt.Sprintf("%s/me/tracks?limit=%d", c.apiBase, config.Page)

	for nextURL != "" {
		data, err := c.do(http.MethodGet, nextURL, nil)
		if err != nil {
			return nil, err
		}

		items, _ := data["items"].([]any)
		for _, item := range items {
			m, _ := item.(map[string]any)
			track, _ := m["track"].(map[string]any)
			uri, _ := track["uri"].(string)
			if uri != "" {
				uris = append(uris, uri)
			}
			if len(uris) >= limit {
				return uris, nil
			}
		}

		nextURL, _ = data["next"].(string)
	}

	return uris, nil
}

func (c *Client) FindPlaylist(name, ownerID string) (id, description string, found bool, err error) {
	nextURL := fmt.Sprintf("%s/me/playlists?limit=%d", c.apiBase, config.Page)

	for nextURL != "" {
		data, err := c.do(http.MethodGet, nextURL, nil)
		if err != nil {
			return "", "", false, err
		}

		items, _ := data["items"].([]any)
		for _, item := range items {
			pl, _ := item.(map[string]any)
			owner, _ := pl["owner"].(map[string]any)
			plName, _ := pl["name"].(string)
			plOwner, _ := owner["id"].(string)
			if plName == name && plOwner == ownerID {
				plID, _ := pl["id"].(string)
				desc, _ := pl["description"].(string)
				return plID, desc, true, nil
			}
		}

		nextURL, _ = data["next"].(string)
	}

	return "", "", false, nil
}

func (c *Client) CreatePlaylist(ownerID, name string) (string, error) {
	result, err := c.do(
		http.MethodPost,
		fmt.Sprintf("%s/users/%s/playlists", c.apiBase, ownerID),
		map[string]any{"name": name, "public": false},
	)
	if err != nil {
		return "", err
	}
	playlistID, _ := result["id"].(string)
	if playlistID == "" {
		return "", fmt.Errorf("unexpected response from create playlist: missing 'id'")
	}
	return playlistID, nil
}

func (c *Client) ChangeDetails(playlistID, description string) error {
	if description == "" {
		return nil
	}
	_, err := c.do(
		http.MethodPut,
		fmt.Sprintf("%s/playlists/%s", c.apiBase, playlistID),
		map[string]any{"description": description},
	)
	return err
}

func (c *Client) ReplaceTracks(playlistID string, uris []string) error {
	if len(uris) == 0 {
		config.Debugf("replace_tracks called with empty uris - skipping")
		return nil
	}

	tracksURL := fmt.Sprintf("%s/playlists/%s/tracks", c.apiBase, playlistID)

	result, err := c.do(http.MethodPut, tracksURL, map[string]any{"uris": uris[:min(len(uris), config.Batch)]})
	if err != nil {
		return err
	}
	if snapshot, _ := result["snapshot_id"].(string); snapshot != "" {
		config.Debugf("replace_tracks snapshot_id=%s", snapshot)
	}

	for start := config.Batch; start < len(uris); start += config.Batch {
		end := min(start+config.Batch, len(uris))
		if _, err := c.do(http.MethodPost, tracksURL, map[string]any{"uris": uris[start:end]}); err != nil {
			return err
		}
	}

	return nil
}

func (c *Client) do(method, url string, body any) (map[string]any, error) {
	if c.token == "" {
		return nil, fmt.Errorf("client not authenticated")
	}

	attempt := 1
	for {
		result, retry, wait, err := c.attempt(method, url, body)
		if err == nil || !retry {
			return result, err
		}
		if attempt >= config.MaxRetries {
			return nil, err
		}
		config.Debugf("%s %s failed (%v); retry %d/%d in %s", method, url, err, attempt, config.MaxRetries, wait)
		if wait > 0 {
			c.sleep(wait)
		}
		attempt++
	}
}

func (c *Client) attempt(method, url string, body any) (result map[string]any, retry bool, wait time.Duration, err error) {
	var bodyReader io.Reader
	if body != nil {
		encoded, encErr := json.Marshal(body)
		if encErr != nil {
			return nil, false, 0, fmt.Errorf("%s %s: encode body: %w", method, url, encErr)
		}
		bodyReader = strings.NewReader(string(encoded))
	}

	req, reqErr := http.NewRequest(method, url, bodyReader)
	if reqErr != nil {
		return nil, false, 0, fmt.Errorf("%s %s: %w", method, url, reqErr)
	}
	req.Header.Set("Authorization", "Bearer "+c.token)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	resp, doErr := c.http.Do(req)
	if doErr != nil {
		return nil, true, config.RetryDelay, fmt.Errorf("%s %s: network error: %w", method, url, doErr)
	}
	defer resp.Body.Close()

	content, _ := io.ReadAll(resp.Body)

	switch {
	case resp.StatusCode == http.StatusUnauthorized:
		if authErr := c.Authenticate(); authErr != nil {
			return nil, false, 0, authErr
		}
		return nil, true, 0, fmt.Errorf("%s %s -> HTTP 401: retrying after token refresh", method, url)

	case resp.StatusCode == http.StatusTooManyRequests:
		return nil, true, c.retryAfter(resp), fmt.Errorf("%s %s -> HTTP 429", method, url)

	case resp.StatusCode >= 500:
		return nil, true, config.RetryDelay,
			fmt.Errorf("%s %s -> HTTP %d: %s", method, url, resp.StatusCode, strings.TrimSpace(string(content)))

	case resp.StatusCode >= 400:
		return nil, false, 0,
			fmt.Errorf("%s %s -> HTTP %d: %s", method, url, resp.StatusCode, strings.TrimSpace(string(content)))
	}

	if len(content) == 0 {
		return map[string]any{}, false, 0, nil
	}

	var decoded map[string]any
	if jsonErr := json.Unmarshal(content, &decoded); jsonErr != nil {
		return nil, false, 0, fmt.Errorf("%s %s -> invalid JSON response", method, url)
	}
	return decoded, false, 0, nil
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
