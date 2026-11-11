package spotify

type Paging[T any] struct {
	Next  string `json:"next"` // nullable: "" on the last page
	Total *int   `json:"total"`
	Items []T    `json:"items"` // nil when missing or null
}

type User struct {
	ID string `json:"id"`
}

type SimplifiedPlaylist struct {
	ID          string        `json:"id"`
	Name        string        `json:"name"`
	Description string        `json:"description"` // nullable
	Owner       PlaylistOwner `json:"owner"`
}

type PlaylistOwner struct {
	ID string `json:"id"`
}

type SavedTrack struct {
	Track Track `json:"track"`
}

type Track struct {
	URI     string             `json:"uri"`
	Name    string             `json:"name"`
	Artists []SimplifiedArtist `json:"artists"`
}

type SimplifiedArtist struct {
	Name string `json:"name"`
}

type Snapshot struct {
	SnapshotID string `json:"snapshot_id"`
}
