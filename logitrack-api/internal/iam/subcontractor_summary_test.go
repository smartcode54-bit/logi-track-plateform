package iam

import (
	"encoding/json"
	"reflect"
	"testing"

	"github.com/google/uuid"

	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/iam/iamdb"
)

func TestSubcontractorSummaryRegions(t *testing.T) {
	for _, regions := range [][]string{{"เชียงใหม่", "ลำพูน"}, {}, nil} {
		t.Run(string(mustJSON(t, regions)), func(t *testing.T) {
			row := iamdb.ListCarrierProfilesRow{ID: uuid.New(), NameTh: "รถร่วม", FleetSize: 50, ServiceRegions: regions}
			summary := subcontractorSummary(row)
			var got map[string]json.RawMessage
			if err := json.Unmarshal(mustJSON(t, summary), &got); err != nil {
				t.Fatal(err)
			}
			want := regions
			if want == nil {
				want = []string{}
			}
			if !reflect.DeepEqual(summary.ServiceRegions, want) || string(got["serviceRegions"]) != string(mustJSON(t, want)) {
				t.Fatalf("serviceRegions = %s, want %s", got["serviceRegions"], mustJSON(t, want))
			}
			if summary.ID != row.ID || summary.FleetSize != 50 || summary.NameTh != row.NameTh {
				t.Fatalf("existing summary fields changed: %+v", summary)
			}
		})
	}
}

func mustJSON(t *testing.T, value any) []byte {
	t.Helper()
	b, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return b
}
