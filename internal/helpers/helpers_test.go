package helpers

import (
	"regexp"
	"strings"
	"testing"
)

func TestComputeURIsHash(t *testing.T) {
	cases := []struct {
		name string
		uris []string
		want string
	}{
		{"empty", []string{}, "e3b0c44298fc"},
		{"single", []string{"spotify:track:abc"}, "9280d0947123"},
		{"multiple", []string{"spotify:track:1", "spotify:track:2", "spotify:track:3"}, "80b4de5b99b7"},
		{"unicode", []string{"spotify:track:é1"}, "27b7b5c15b6a"},
	}

	hexRe := regexp.MustCompile(`^[0-9a-f]{12}$`)

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := ComputeURIsHash(tc.uris)
			if got != tc.want {
				t.Errorf("ComputeURIsHash(%v) = %q, want %q", tc.uris, got, tc.want)
			}
			if !hexRe.MatchString(got) {
				t.Errorf("ComputeURIsHash(%v) = %q, want 12 lowercase hex chars", tc.uris, got)
			}
		})
	}
}

func TestComputeURIsHashOrderMatters(t *testing.T) {
	uris := []string{"spotify:track:1", "spotify:track:2", "spotify:track:3"}
	reversed := []string{"spotify:track:3", "spotify:track:2", "spotify:track:1"}

	got := ComputeURIsHash(uris)
	gotReversed := ComputeURIsHash(reversed)

	if got == gotReversed {
		t.Errorf("expected different digests for reversed order, both got %q", got)
	}
}

func TestParseHash(t *testing.T) {
	cases := []struct {
		name        string
		description string
		wantHash    string
		wantOK      bool
	}{
		{
			"embedded in full description",
			"100 most recently liked songs. Auto-updated 2026-01-02 03:04 UTC. [#e3b0c44298fc]",
			"e3b0c44298fc",
			true,
		},
		{"bare tag", "[#abcdef012345]", "abcdef012345", true},
		{"surrounded by text", "prefix text [#abcdef012345] suffix text", "abcdef012345", true},
		{"no hash", "no hash here at all", "", false},
		{"too short - 11 hex chars", "[#abcdef01234]", "", false},
		{"too long - 13 hex chars", "[#abcdef0123456]", "", false},
		{"uppercase hex", "[#ABCDEF012345]", "", false},
		{"empty string", "", "", false},
		{"non-hex char", "[#abcdef01234g]", "", false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			gotHash, gotOK := ParseHash(tc.description)
			if gotHash != tc.wantHash || gotOK != tc.wantOK {
				t.Errorf("ParseHash(%q) = (%q, %v), want (%q, %v)",
					tc.description, gotHash, gotOK, tc.wantHash, tc.wantOK)
			}
		})
	}
}

func TestBuildDescription(t *testing.T) {
	digest := "e3b0c44298fc"
	desc := BuildDescription(100, digest)

	if !strings.Contains(desc, "100 most recently liked songs.") {
		t.Errorf("BuildDescription result %q missing expected count/label prefix", desc)
	}
	if !strings.Contains(desc, "[#"+digest+"]") {
		t.Errorf("BuildDescription result %q missing expected hash tag [#%s]", desc, digest)
	}
	if !strings.Contains(desc, " UTC.") {
		t.Errorf("BuildDescription result %q missing ' UTC.' marker", desc)
	}

	tsRe := regexp.MustCompile(`\d{4}-\d{2}-\d{2} \d{2}:\d{2}`)
	if !tsRe.MatchString(desc) {
		t.Errorf("BuildDescription result %q missing timestamp matching YYYY-MM-DD HH:MM", desc)
	}
}

func TestBuildDescriptionRoundTripsWithParseHash(t *testing.T) {
	digest := "e3b0c44298fc"
	desc := BuildDescription(100, digest)

	gotHash, gotOK := ParseHash(desc)
	if !gotOK || gotHash != digest {
		t.Errorf("ParseHash(BuildDescription(100, %q)) = (%q, %v), want (%q, true)",
			digest, gotHash, gotOK, digest)
	}
}

func TestStr2Bool(t *testing.T) {
	trueVals := []string{"1", "yes", "Yes", "YES", "y", "Y", "true", "True", "TRUE", "t"}
	falseVals := []string{"0", "no", "No", "NO", "n", "N", "false", "False", "FALSE", "f", ""}
	badVals := []string{"maybe", "2", "yess"}

	for _, v := range trueVals {
		t.Run("true_"+v, func(t *testing.T) {
			got, err := Str2Bool(v)
			if err != nil {
				t.Errorf("Str2Bool(%q) returned error %v, want nil", v, err)
			}
			if got != true {
				t.Errorf("Str2Bool(%q) = %v, want true", v, got)
			}
		})
	}

	for _, v := range falseVals {
		t.Run("false_"+v, func(t *testing.T) {
			got, err := Str2Bool(v)
			if err != nil {
				t.Errorf("Str2Bool(%q) returned error %v, want nil", v, err)
			}
			if got != false {
				t.Errorf("Str2Bool(%q) = %v, want false", v, got)
			}
		})
	}

	t.Run("whitespace_trimmed_true", func(t *testing.T) {
		got, err := Str2Bool(" true ")
		if err != nil {
			t.Errorf("Str2Bool(%q) returned error %v, want nil", " true ", err)
		}
		if got != true {
			t.Errorf("Str2Bool(%q) = %v, want true", " true ", got)
		}
	})

	for _, v := range badVals {
		t.Run("bad_"+v, func(t *testing.T) {
			got, err := Str2Bool(v)
			if err == nil {
				t.Errorf("Str2Bool(%q) returned nil error, want non-nil", v)
			}
			if got != false {
				t.Errorf("Str2Bool(%q) = %v, want false on error", v, got)
			}
		})
	}
}
