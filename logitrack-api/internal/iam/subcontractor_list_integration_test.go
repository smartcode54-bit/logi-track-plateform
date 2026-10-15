//go:build integration

package iam_test

import (
	"net/http"
	"reflect"
	"testing"
)

func TestSubcontractorListServiceRegions(t *testing.T) {
	w := newWorld(t)
	w.exec(`UPDATE tenants SET service_regions = ARRAY['เชียงใหม่', 'ลำพูน'] WHERE id = $1`, w.A)
	w.exec(`UPDATE tenants SET service_regions = '{}' WHERE id = $1`, w.C)
	token := w.login("ta.o@example.test")
	r := w.get("/v1/subcontractors?limit=500", token)
	expect(t, r, http.StatusOK, "")
	want := map[string][]any{w.A: {"เชียงใหม่", "ลำพูน"}, w.C: {}}
	for _, item := range r.data().([]any) {
		row := item.(map[string]any)
		id := row["id"].(string)
		if regions, ok := want[id]; ok {
			if !reflect.DeepEqual(row["serviceRegions"], regions) {
				t.Errorf("carrier %s serviceRegions = %v, want %v", id, row["serviceRegions"], regions)
			}
			delete(want, id)
		}
	}
	if len(want) != 0 {
		t.Errorf("carriers missing from list: %v", want)
	}
	r = w.get("/v1/subcontractors?fields=minimal&limit=500", token)
	expect(t, r, http.StatusOK, "")
	for _, item := range r.data().([]any) {
		if _, exists := item.(map[string]any)["serviceRegions"]; exists {
			t.Error("minimal picker projection unexpectedly exposes serviceRegions")
		}
	}
}
