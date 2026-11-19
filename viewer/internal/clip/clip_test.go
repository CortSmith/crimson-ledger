package clip

import (
	"errors"
	"testing"
)

func TestCopyWith_NoToolFoundIsUnavailable(t *testing.T) {
	none := func(string) (string, error) { return "", errors.New("not found") }
	for _, goos := range []string{"linux", "darwin", "windows"} {
		if err := copyWith(goos, none, "x"); !errors.Is(err, ErrUnavailable) {
			t.Errorf("%s: %v", goos, err)
		}
	}
}

func TestCandidates_LinuxPrefersWaylandThenXclip(t *testing.T) {
	c := candidates("linux")
	if c[0][0] != "wl-copy" || c[1][0] != "xclip" {
		t.Fatalf("%v", c)
	}
}
