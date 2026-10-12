package storage_test

import (
	"encoding/json"
	"os"
	"testing"

	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/golden"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/storage"
)

// galleryGolden is testdata/golden/evidence/pages.json, exported from fn:tripEvidence.ts by export.mjs.
type galleryGolden struct {
	Status map[string]string `json:"status"`
	Cases  []struct {
		Name     string `json:"name"`
		Title    string `json:"title"`
		Subtitle struct {
			Origin, Destination, TripNo, Start, End *string
		} `json:"subtitle"`
		Photos []storage.GalleryPhoto `json:"photos"`
		HTML   string                 `json:"html"`
	} `json:"cases"`
	Labels map[string]string `json:"labels"`
}

// T49 AC "Gallery 400/404/500 pages preserved": the status pages, the galleries and the photo labels are the legacy
// function's, byte for byte.
func TestGalleryPagesKeepTheLegacyHTML(t *testing.T) {
	raw, err := os.ReadFile(golden.Path(t, "evidence/pages.json"))
	if err != nil {
		t.Fatal(err)
	}
	var g galleryGolden
	if err := json.Unmarshal(raw, &g); err != nil {
		t.Fatal(err)
	}
	for status, page := range map[string]string{"400": storage.GalleryBadRequestPage, "404": storage.GalleryNotFoundPage,
		"500": storage.GalleryErrorPage} {
		if page != g.Status[status] {
			t.Errorf("%s page\n got %s\nwant %s", status, page, g.Status[status])
		}
	}
	if len(g.Cases) != 3 {
		t.Fatalf("%d galleries", len(g.Cases))
	}
	for _, c := range g.Cases {
		gal := storage.Gallery{Title: c.Title, Photos: c.Photos}
		if c.Subtitle.TripNo != nil {
			gal.Subtitle = orDash(c.Subtitle.Origin) + " → " + orDash(c.Subtitle.Destination) + " · เลขทริป " + *c.Subtitle.TripNo
		} else {
			gal.Subtitle = orDash(c.Subtitle.Start) + " → " + orDash(c.Subtitle.End) + " · งานหมด / Standby"
		}
		if got := storage.RenderGallery(gal); got != c.HTML {
			t.Errorf("%s\n got %s\nwant %s", c.Name, got, c.HTML)
		}
	}
	for typ, want := range g.Labels {
		if got := storage.PhotoLabel(typ); got != want {
			t.Errorf("label %q = %q, want %q", typ, got, want)
		}
	}
}

func orDash(v *string) string {
	if v == nil {
		return "-"
	}
	return *v
}
