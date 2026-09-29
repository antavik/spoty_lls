package app

import (
	"errors"
	"reflect"
	"strings"
	"testing"

	"spoty_lls/internal/config"
	"spoty_lls/internal/helpers"
)

// fakeClient implements SpotifyAPI with canned return values and call recorders.
type fakeClient struct {
	authErr error

	userID  string
	userErr error

	liked         []string
	likedErr      error
	likedLimitGot int
	likedCalled   bool

	findID       string
	findDesc     string
	findFound    bool
	findErr      error
	findCalled   bool
	findNameGot  string
	findOwnerGot string

	createID       string
	createErr      error
	createCalled   bool
	createOwnerGot string
	createNameGot  string

	replaceErr           error
	replaceCalled        bool
	replacePlaylistIDGot string
	replaceURIsGot       []string

	changeErr           error
	changeCalled        bool
	changePlaylistIDGot string
	changeDescGot       string
}

var _ SpotifyAPI = (*fakeClient)(nil)

// testCfg builds a Config with the default liked limit for tests that don't
// exercise SPOTIFY_LIKED_LIMIT specifically.
func testCfg() config.Config {
	return config.Config{LikedLimit: config.DefaultLikedLimit}
}

func (f *fakeClient) Authenticate() error {
	return f.authErr
}

func (f *fakeClient) CurrentUserID() (string, error) {
	if f.userErr != nil {
		return "", f.userErr
	}
	return f.userID, nil
}

func (f *fakeClient) LikedTrackURIs(limit int) ([]string, error) {
	f.likedCalled = true
	f.likedLimitGot = limit
	if f.likedErr != nil {
		return nil, f.likedErr
	}
	return f.liked, nil
}

func (f *fakeClient) FindPlaylist(name, ownerID string) (string, string, bool, error) {
	f.findCalled = true
	f.findNameGot = name
	f.findOwnerGot = ownerID
	if f.findErr != nil {
		return "", "", false, f.findErr
	}
	return f.findID, f.findDesc, f.findFound, nil
}

func (f *fakeClient) CreatePlaylist(ownerID, name string) (string, error) {
	f.createCalled = true
	f.createOwnerGot = ownerID
	f.createNameGot = name
	if f.createErr != nil {
		return "", f.createErr
	}
	return f.createID, nil
}

func (f *fakeClient) ReplaceTracks(playlistID string, uris []string) error {
	f.replaceCalled = true
	f.replacePlaylistIDGot = playlistID
	f.replaceURIsGot = uris
	return f.replaceErr
}

func (f *fakeClient) ChangeDetails(playlistID, description string) error {
	f.changeCalled = true
	f.changePlaylistIDGot = playlistID
	f.changeDescGot = description
	return f.changeErr
}

func TestRunLikedEmpty(t *testing.T) {
	client := &fakeClient{userID: "user1", liked: []string{}}

	notifyCalls := 0
	err := Run(testCfg(), client, func(string) { notifyCalls++ })

	if err != nil {
		t.Fatalf("Run() error = %v, want nil", err)
	}
	if client.findCalled {
		t.Errorf("FindPlaylist called, want not called when liked list is empty")
	}
	if client.createCalled {
		t.Errorf("CreatePlaylist called, want not called when liked list is empty")
	}
	if client.replaceCalled {
		t.Errorf("ReplaceTracks called, want not called when liked list is empty")
	}
	if client.changeCalled {
		t.Errorf("ChangeDetails called, want not called when liked list is empty")
	}
	if notifyCalls != 0 {
		t.Errorf("notify called %d times, want 0", notifyCalls)
	}
}

func TestRunUnchanged(t *testing.T) {
	liked := []string{"spotify:track:1", "spotify:track:2"}
	digest := helpers.ComputeURIsHash(liked)
	desc := helpers.BuildDescription(len(liked), digest)

	client := &fakeClient{
		userID:    "user1",
		liked:     liked,
		findID:    "pl1",
		findDesc:  desc,
		findFound: true,
	}

	notifyCalls := 0
	err := Run(testCfg(), client, func(string) { notifyCalls++ })

	if err != nil {
		t.Fatalf("Run() error = %v, want nil", err)
	}
	if !client.findCalled {
		t.Errorf("FindPlaylist not called, want called")
	}
	if client.findNameGot != config.PlaylistName {
		t.Errorf("FindPlaylist name = %q, want %q", client.findNameGot, config.PlaylistName)
	}
	if client.findOwnerGot != "user1" {
		t.Errorf("FindPlaylist owner = %q, want %q", client.findOwnerGot, "user1")
	}
	if client.createCalled {
		t.Errorf("CreatePlaylist called, want not called when playlist found")
	}
	if client.replaceCalled {
		t.Errorf("ReplaceTracks called, want not called when hash unchanged")
	}
	if client.changeCalled {
		t.Errorf("ChangeDetails called, want not called when hash unchanged")
	}
	if notifyCalls != 0 {
		t.Errorf("notify called %d times, want 0", notifyCalls)
	}
}

func TestRunChanged(t *testing.T) {
	liked := []string{"spotify:track:1", "spotify:track:2"}
	digest := helpers.ComputeURIsHash(liked)

	staleVariants := map[string]string{
		"empty_description":  "",
		"stale_hash_present": "[#000000000000]",
	}

	for name, staleDesc := range staleVariants {
		t.Run(name, func(t *testing.T) {
			client := &fakeClient{
				userID:    "user1",
				liked:     liked,
				findID:    "pl1",
				findDesc:  staleDesc,
				findFound: true,
				createID:  "unused",
			}

			notifyCalls := 0
			err := Run(testCfg(), client, func(string) { notifyCalls++ })

			if err != nil {
				t.Fatalf("Run() error = %v, want nil", err)
			}
			if client.createCalled {
				t.Errorf("CreatePlaylist called, want not called when playlist found")
			}
			if !client.replaceCalled {
				t.Fatalf("ReplaceTracks not called, want called")
			}
			if client.replacePlaylistIDGot != "pl1" {
				t.Errorf("ReplaceTracks playlistID = %q, want %q", client.replacePlaylistIDGot, "pl1")
			}
			if !reflect.DeepEqual(client.replaceURIsGot, liked) {
				t.Errorf("ReplaceTracks uris = %v, want %v", client.replaceURIsGot, liked)
			}
			if !client.changeCalled {
				t.Fatalf("ChangeDetails not called, want called")
			}
			if client.changePlaylistIDGot != "pl1" {
				t.Errorf("ChangeDetails playlistID = %q, want %q", client.changePlaylistIDGot, "pl1")
			}
			gotHash, ok := helpers.ParseHash(client.changeDescGot)
			if !ok || gotHash != digest {
				t.Errorf("ParseHash(ChangeDetails description) = (%q, %v), want (%q, true)", gotHash, ok, digest)
			}
			if !strings.Contains(client.changeDescGot, "2 most recently liked songs.") {
				t.Errorf("ChangeDetails description %q missing %q", client.changeDescGot, "2 most recently liked songs.")
			}
			if notifyCalls != 0 {
				t.Errorf("notify called %d times, want 0", notifyCalls)
			}
		})
	}
}

func TestRunPlaylistMissing(t *testing.T) {
	liked := []string{"spotify:track:1", "spotify:track:2"}
	digest := helpers.ComputeURIsHash(liked)

	client := &fakeClient{
		userID:    "user1",
		liked:     liked,
		findFound: false,
		createID:  "newpl",
	}

	notifyCalls := 0
	err := Run(testCfg(), client, func(string) { notifyCalls++ })

	if err != nil {
		t.Fatalf("Run() error = %v, want nil", err)
	}
	if !client.createCalled {
		t.Fatalf("CreatePlaylist not called, want called when playlist not found")
	}
	if client.createOwnerGot != "user1" {
		t.Errorf("CreatePlaylist owner = %q, want %q", client.createOwnerGot, "user1")
	}
	if client.createNameGot != config.PlaylistName {
		t.Errorf("CreatePlaylist name = %q, want %q", client.createNameGot, config.PlaylistName)
	}
	if !client.replaceCalled {
		t.Fatalf("ReplaceTracks not called, want called")
	}
	if client.replacePlaylistIDGot != "newpl" {
		t.Errorf("ReplaceTracks playlistID = %q, want %q", client.replacePlaylistIDGot, "newpl")
	}
	if !reflect.DeepEqual(client.replaceURIsGot, liked) {
		t.Errorf("ReplaceTracks uris = %v, want %v", client.replaceURIsGot, liked)
	}
	if !client.changeCalled {
		t.Fatalf("ChangeDetails not called, want called")
	}
	if client.changePlaylistIDGot != "newpl" {
		t.Errorf("ChangeDetails playlistID = %q, want %q", client.changePlaylistIDGot, "newpl")
	}
	gotHash, ok := helpers.ParseHash(client.changeDescGot)
	if !ok || gotHash != digest {
		t.Errorf("ParseHash(ChangeDetails description) = (%q, %v), want (%q, true)", gotHash, ok, digest)
	}
	if notifyCalls != 0 {
		t.Errorf("notify called %d times, want 0", notifyCalls)
	}
}

func TestRunNotifyOnFailure(t *testing.T) {
	baseline := func() *fakeClient {
		return &fakeClient{
			userID:    "user1",
			liked:     []string{"spotify:track:1"},
			findID:    "pl1",
			findDesc:  "", // stale/absent -> triggers replace+change path
			findFound: true,
			createID:  "newpl",
		}
	}

	cases := []struct {
		name      string
		injectMsg string
		modify    func(c *fakeClient, errText string)
	}{
		{
			name:      "Authenticate",
			injectMsg: "auth boom",
			modify: func(c *fakeClient, errText string) {
				c.authErr = errors.New(errText)
			},
		},
		{
			name:      "CurrentUserID",
			injectMsg: "user boom",
			modify: func(c *fakeClient, errText string) {
				c.userErr = errors.New(errText)
			},
		},
		{
			name:      "LikedTrackURIs",
			injectMsg: "liked boom",
			modify: func(c *fakeClient, errText string) {
				c.likedErr = errors.New(errText)
			},
		},
		{
			name:      "FindPlaylist",
			injectMsg: "find boom",
			modify: func(c *fakeClient, errText string) {
				c.findErr = errors.New(errText)
			},
		},
		{
			name:      "CreatePlaylist",
			injectMsg: "create boom",
			modify: func(c *fakeClient, errText string) {
				c.findFound = false
				c.createErr = errors.New(errText)
			},
		},
		{
			name:      "ReplaceTracks",
			injectMsg: "replace boom",
			modify: func(c *fakeClient, errText string) {
				c.replaceErr = errors.New(errText)
			},
		},
		{
			name:      "ChangeDetails",
			injectMsg: "change boom",
			modify: func(c *fakeClient, errText string) {
				c.changeErr = errors.New(errText)
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			client := baseline()
			tc.modify(client, tc.injectMsg)

			var notifyMsgs []string
			notify := func(msg string) { notifyMsgs = append(notifyMsgs, msg) }

			err := Run(testCfg(), client, notify)

			if err == nil {
				t.Fatalf("Run() error = nil, want non-nil")
			}
			if len(notifyMsgs) != 1 {
				t.Fatalf("notify called %d times, want exactly 1 (msgs=%v)", len(notifyMsgs), notifyMsgs)
			}
			if !strings.HasPrefix(notifyMsgs[0], "spoty_lls failed:") {
				t.Errorf("notify message = %q, want prefix %q", notifyMsgs[0], "spoty_lls failed:")
			}
			if !strings.Contains(notifyMsgs[0], tc.injectMsg) {
				t.Errorf("notify message = %q, want it to contain injected error text %q", notifyMsgs[0], tc.injectMsg)
			}
		})
	}
}

func TestRunCallsLikedWithLimit(t *testing.T) {
	client := &fakeClient{userID: "user1", liked: []string{}}

	err := Run(testCfg(), client, func(string) {})

	if err != nil {
		t.Fatalf("Run() error = %v, want nil", err)
	}
	if !client.likedCalled {
		t.Fatalf("LikedTrackURIs not called, want called")
	}
	if client.likedLimitGot != config.DefaultLikedLimit {
		t.Errorf("LikedTrackURIs limit = %d, want %d (config.DefaultLikedLimit)", client.likedLimitGot, config.DefaultLikedLimit)
	}
}

func TestRunUsesCfgLikedLimit(t *testing.T) {
	client := &fakeClient{userID: "user1", liked: []string{}}
	cfg := config.Config{LikedLimit: 42}

	err := Run(cfg, client, func(string) {})

	if err != nil {
		t.Fatalf("Run() error = %v, want nil", err)
	}
	if !client.likedCalled {
		t.Fatalf("LikedTrackURIs not called, want called")
	}
	if client.likedLimitGot != 42 {
		t.Errorf("LikedTrackURIs limit = %d, want 42 (cfg.LikedLimit)", client.likedLimitGot)
	}
}

func TestNotifyTelegramNoop(t *testing.T) {
	// No-op cases must not panic and must not attempt any network request.
	NotifyTelegram("", "", "x")
	NotifyTelegram("tok", "", "x")
}
