package app

import (
	"fmt"
	"log"

	"spoty_lls/internal/config"
	"spoty_lls/internal/helpers"
)

type SpotifyAPI interface {
	Authenticate() error
	CurrentUserID() (string, error)
	LikedTrackURIs(limit int) ([]string, error)
	FindPlaylist(name, ownerID string) (id, description string, found bool, err error)
	CreatePlaylist(ownerID, name string) (string, error)
	ReplaceTracks(playlistID string, uris []string) error
	ChangeDetails(playlistID, description string) error
}

func Run(client SpotifyAPI, notify func(string)) error {
	if err := sync(client); err != nil {
		msg := fmt.Sprintf("spoty_lls failed: %v", err)
		notify(msg)
		log.Print(msg)
		return err
	}
	return nil
}

func sync(client SpotifyAPI) error {
	if err := client.Authenticate(); err != nil {
		return fmt.Errorf("authenticate: %w", err)
	}

	userID, err := client.CurrentUserID()
	if err != nil {
		return err
	}
	config.Debugf("get current user id: %s", userID)

	liked, err := client.LikedTrackURIs(config.LikedLimit)
	if err != nil {
		return err
	}
	if len(liked) == 0 {
		log.Print("no liked tracks found, skipping playlist update")
		return nil
	}
	config.Debugf("get %d liked tracks", len(liked))

	digest := helpers.ComputeURIsHash(liked)
	playlistID, description, found, err := client.FindPlaylist(config.PlaylistName, userID)
	if err != nil {
		return err
	}

	var storedHash string
	if !found {
		playlistID, err = client.CreatePlaylist(userID, config.PlaylistName)
		if err != nil {
			return err
		}
		log.Printf("created playlist '%s' (%s)", config.PlaylistName, playlistID)
	} else {
		storedHash, _ = helpers.ParseHash(description)
		config.Debugf("found playlist '%s' (%s)", config.PlaylistName, playlistID)
	}

	if storedHash == digest {
		log.Printf("liked songs unchanged (hash %s), skipping update", digest)
		return nil
	}

	if err := client.ReplaceTracks(playlistID, liked); err != nil {
		return err
	}
	config.Debugf("replaced tracks in playlist '%s' (%s)", config.PlaylistName, playlistID)

	if err := client.ChangeDetails(playlistID, helpers.BuildDescription(len(liked), digest)); err != nil {
		return err
	}
	config.Debugf("updated playlist description for '%s' (%s)", config.PlaylistName, playlistID)

	log.Print("done")
	return nil
}
