package app

import (
	"fmt"

	"github.com/go-pkgz/lgr"

	"spoty_lls/internal/config"
	"spoty_lls/internal/helpers"
)

type SpotifyAPI interface {
	Authenticate() error
	CurrentUserID() (string, error)
	LikedTracks(limit int) ([]helpers.Track, error)
	FindPlaylist(name, ownerID string) (id, description string, found bool, err error)
	CreatePlaylist(ownerID, name string) (string, error)
	ReplaceTracks(playlistID string, tracks []helpers.Track) error
	ChangeDetails(playlistID, description string) error
}

func Run(cfg config.Config, client SpotifyAPI, notify func(string)) error {
	if err := sync(cfg, client); err != nil {
		msg := fmt.Sprintf("spoty_lls failed: %v", err)
		notify(msg)
		lgr.Printf("[ERROR] %s", msg)
		return err
	}
	return nil
}

func sync(cfg config.Config, client SpotifyAPI) error {
	if err := client.Authenticate(); err != nil {
		return fmt.Errorf("authenticate: %w", err)
	}

	userID, err := client.CurrentUserID()
	if err != nil {
		return err
	}
	lgr.Printf("[DEBUG] get current user id: %s", userID)

	liked, err := client.LikedTracks(cfg.LikedLimit)
	if err != nil {
		return err
	}
	if len(liked) == 0 {
		lgr.Print("[WARN] no liked tracks found, skipping playlist update")
		return nil
	}
	lgr.Printf("[DEBUG] get %d liked tracks", len(liked))

	digest := helpers.ComputeTracksHash(liked)
	playlistID, description, found, err := client.FindPlaylist(cfg.PlaylistName, userID)
	if err != nil {
		return err
	}

	var storedHash string
	if !found {
		playlistID, err = client.CreatePlaylist(userID, cfg.PlaylistName)
		if err != nil {
			return err
		}
		lgr.Printf("[INFO] created playlist '%s' (%s)", cfg.PlaylistName, playlistID)
	} else {
		storedHash, _ = helpers.ParseHash(description)
		lgr.Printf("[DEBUG] found playlist '%s' (%s)", cfg.PlaylistName, playlistID)
	}

	if storedHash == digest {
		lgr.Printf("[INFO] liked songs unchanged (hash %s), skipping update", digest)
		return nil
	}

	if err := client.ReplaceTracks(playlistID, liked); err != nil {
		return err
	}
	lgr.Printf("[DEBUG] replaced tracks in playlist '%s' (%s)", cfg.PlaylistName, playlistID)

	if err := client.ChangeDetails(playlistID, helpers.BuildDescription(len(liked), digest)); err != nil {
		return err
	}
	lgr.Printf("[DEBUG] updated playlist description for '%s' (%s)", cfg.PlaylistName, playlistID)

	lgr.Print("[INFO] done")
	return nil
}
