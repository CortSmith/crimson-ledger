package maps

import (
	"errors"
	"fmt"
	"strings"
	"testing"
	"testing/fstest"

	bundle "crimsonledger/viewer/maps"
)

func fixture(n int) fstest.MapFS {
	idx := `{"zones":{`
	fsys := fstest.MapFS{}
	for i := 0; i < n; i++ {
		if i > 0 {
			idx += ","
		}
		idx += fmt.Sprintf(`"zone%d":{"name":"Zone %d","file":"Z%d.png"}`, i, i, i)
		fsys[fmt.Sprintf("Z%d.png", i)] = &fstest.MapFile{Data: []byte("img")}
	}
	idx += `,"evil":{"name":"Evil","file":"../secret.png"}}}`
	fsys["index.json"] = &fstest.MapFile{Data: []byte(idx)}
	return fsys
}

func TestDataURI_EncodesImageAndPrefersWebP(t *testing.T) {
	f := fixture(1)
	f["Z0.webp"] = &fstest.MapFile{Data: []byte("webp")}
	s, _ := Open(f)
	got, err := s.DataURI("Zone 0")
	if err != nil || !strings.HasPrefix(got, "data:image/webp;base64,") {
		t.Fatalf("got %q err %v", got, err)
	}
}

func TestDataURI_UnknownZoneAndPathTraversalAreNotFound(t *testing.T) {
	s, _ := Open(fixture(1))
	for _, z := range []string{"Nowhere", "Evil", "../secret"} {
		if _, err := s.DataURI(z); !errors.Is(err, ErrNotFound) {
			t.Errorf("%q: err %v", z, err)
		}
	}
}

func TestMaps_DataURI_LRU(t *testing.T) {
	s, _ := Open(fixture(12))
	for i := 0; i < 12; i++ {
		if _, err := s.DataURI(fmt.Sprintf("zone%d", i)); err != nil {
			t.Fatal(err)
		}
	}
	if s.Cached() != cacheSize {
		t.Fatalf("cached %d, want %d", s.Cached(), cacheSize)
	}
}

func TestOpen_MissingIndexIsAnError(t *testing.T) {
	if _, err := Open(fstest.MapFS{}); err == nil {
		t.Fatal("want error")
	}
}

func TestBundledMaps_EveryIndexedZoneHasAnImage(t *testing.T) {
	s, err := Open(bundle.FS)
	if err != nil {
		t.Fatal(err)
	}
	for key, e := range s.zones {
		if !s.Has(e.Name) {
			t.Errorf("zone %s (%s) has no embedded image", key, e.File)
		}
	}
}

// TestBundle_EveryZoneHasAnImage walks the real embedded index: each entry must load as a data: URI and be
// reachable by its display name, so a zone can never be listed without a picture.
func TestBundle_EveryZoneHasAnImage(t *testing.T) {
	s, err := Open(bundle.FS)
	if err != nil {
		t.Fatal(err)
	}
	if len(s.zones) == 0 {
		t.Fatal("bundled index has no zones")
	}
	for key, e := range s.zones {
		t.Run(e.Name, func(t *testing.T) {
			if Normalize(e.Name) != key {
				t.Errorf("key %q is not the normalized name %q", key, e.Name)
			}
			if !s.Has(e.Name) {
				t.Fatalf("no image file for %q (index file %q)", e.Name, e.File)
			}
			uri, err := s.DataURI(e.Name)
			if err != nil || !strings.HasPrefix(uri, "data:image/") {
				t.Errorf("DataURI(%q) = %.30q, %v", e.Name, uri, err)
			}
		})
	}
}
