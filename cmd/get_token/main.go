package main

import (
	"bufio"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"strings"

	"spoty_lls/internal/config"
)

func main() {
	cfg, err := config.Load()
	if err != nil {
		log.Fatalf("spoty_lls get_token: %v", err)
	}

	params := url.Values{
		"client_id":     {cfg.ClientID},
		"response_type": {"code"},
		"redirect_uri":  {cfg.RedirectURI},
		"scope":         {config.Scopes},
	}

	fmt.Print("\n1) Open this URL in your browser and click 'Agree':\n\n")
	fmt.Printf("%s?%s\n", config.AuthorizeURL, params.Encode())
	fmt.Println("\n2) Your browser will redirect to a URL that may look 'broken' (page not found) - that's fine.")
	fmt.Print("   Copy the FULL redirected URL from the address bar and paste it here.\n\n")
	fmt.Print("Redirected URL: ")

	redirected, _ := bufio.NewReader(os.Stdin).ReadString('\n')
	redirected = strings.TrimSpace(redirected)

	parsed, err := url.Parse(redirected)
	if err != nil {
		log.Fatalf("could not parse redirected URL: %v", err)
	}
	qs := parsed.Query()
	code := qs.Get("code")
	if code == "" {
		errText := qs.Get("error")
		if errText == "" {
			errText = "unknown"
		}
		log.Fatalf("no 'code' in URL (error: %s). Full query: %v", errText, qs)
	}

	form := url.Values{
		"grant_type":   {"authorization_code"},
		"code":         {code},
		"redirect_uri": {cfg.RedirectURI},
	}
	req, err := http.NewRequest(http.MethodPost, config.TokenURL, strings.NewReader(form.Encode()))
	if err != nil {
		log.Fatalf("token exchange failed: %v", err)
	}
	basic := base64.StdEncoding.EncodeToString([]byte(cfg.ClientID + ":" + cfg.ClientSecret))
	req.Header.Set("Authorization", "Basic "+basic)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	client := &http.Client{Timeout: config.RequestTimeout}
	resp, err := client.Do(req)
	if err != nil {
		log.Fatalf("token exchange failed: %v", err)
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode >= 400 {
		log.Fatalf("token exchange failed: HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}

	var payload struct {
		RefreshToken string `json:"refresh_token"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		log.Fatal("token exchange failed: invalid JSON response")
	}
	if payload.RefreshToken == "" {
		log.Fatal("no 'refresh_token' in response. Check scopes and credentials.")
	}

	fmt.Print("\n=== Save this as SPOTIFY_REFRESH_TOKEN ===\n\n")
	fmt.Println(payload.RefreshToken)
	fmt.Println()
}
