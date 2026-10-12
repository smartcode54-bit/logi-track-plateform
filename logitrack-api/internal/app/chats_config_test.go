package app_test

import (
	"strings"
	"testing"

	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/app"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/config"
)

// The chat write-back (T47) shares WRITEBACK_FIRESTORE_PROJECT_ID with the operations write-back (T31) and the leave
// decisions (T45): the project needs GOOGLE_APPLICATION_CREDENTIALS while operations or comms is Firestore-owned, or
// any domain is (the leave decisions), and nothing once every domain is PostgreSQL-owned; CommsOwned follows
// PG_OWNED_DOMAINS.
func TestWriteBackCredentialsFollowComms(t *testing.T) {
	for _, tc := range []struct {
		owned       string
		needsCreds  bool
		commsOwned  bool
		withProject bool
	}{
		{owned: "", needsCreds: true, withProject: true},
		{owned: "operations", needsCreds: true, withProject: true},
		{owned: "comms", needsCreds: true, commsOwned: true, withProject: true},
		{owned: "operations,comms", needsCreds: true, commsOwned: true, withProject: true},
		{owned: "all", commsOwned: true, withProject: true},
		{owned: "auth", withProject: false},
	} {
		extra := []string{"PG_OWNED_DOMAINS=" + tc.owned}
		if tc.withProject {
			extra = append(extra, "WRITEBACK_FIRESTORE_PROJECT_ID=example-writeback")
		}
		cfg, err := config.LoadFrom[app.APIConfig](override(baseEnv(), extra))
		if tc.needsCreds {
			if err == nil || !strings.Contains(err.Error(), "GOOGLE_APPLICATION_CREDENTIALS") {
				t.Errorf("%q: want the credentials error, got %v", tc.owned, err)
			}
			continue
		}
		if err != nil {
			t.Errorf("%q: %v", tc.owned, err)
			continue
		}
		if cfg.CommsOwned != tc.commsOwned {
			t.Errorf("%q: CommsOwned %v", tc.owned, cfg.CommsOwned)
		}
	}
}
