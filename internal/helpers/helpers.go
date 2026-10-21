package helpers

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"regexp"
	"strings"
	"time"
)

var hashRe = regexp.MustCompile(`\[#([0-9a-f]{12})\]`)

type Track struct {
	URI     string
	Title   string
	Artists []string
}

func ComputeTracksHash(tracks []Track) string {
	keys := make([]string, len(tracks))
	for i, t := range tracks {
		keys[i] = strings.Join(t.Artists, ", ") + " - " + t.Title
	}
	sum := sha256.Sum256([]byte(strings.Join(keys, "\n")))
	return hex.EncodeToString(sum[:])[:12]
}

func URIs(tracks []Track) []string {
	uris := make([]string, len(tracks))
	for i, t := range tracks {
		uris[i] = t.URI
	}
	return uris
}

func ParseHash(description string) (string, bool) {
	m := hashRe.FindStringSubmatch(description)
	if m == nil {
		return "", false
	}
	return m[1], true
}

func BuildDescription(count int, digest string) string {
	ts := time.Now().UTC().Format("2006-01-02 15:04")
	return fmt.Sprintf("%d most recently liked songs. Auto-updated %s UTC. [#%s]", count, ts, digest)
}

var (
	trueVals  = map[string]bool{"1": true, "yes": true, "Yes": true, "YES": true, "y": true, "Y": true, "true": true, "True": true, "TRUE": true, "t": true}
	falseVals = map[string]bool{"0": true, "no": true, "No": true, "NO": true, "n": true, "N": true, "false": true, "False": true, "FALSE": true, "f": true, "": true}
)

func Str2Bool(s string) (bool, error) {
	v := strings.TrimSpace(s)
	if trueVals[v] {
		return true, nil
	}
	if falseVals[v] {
		return false, nil
	}
	return false, fmt.Errorf("unsupported string value for bool cast: %q", s)
}
