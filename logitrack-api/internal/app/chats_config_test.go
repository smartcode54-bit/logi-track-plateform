package app_test

import (
	"strings"
	"testing"

	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/app"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/config"
)

// The chat write-back (T47) shares WRITEBACK_FIRESTORE_PROJECT_ID with the operations write-back (T31) and the leave
// decisions (T45): the project needs GOOGLE_APPLICATION_CREDENTIALS while operations is Firestore-owned or any domain is
// (chats and leave requests are class C until P7b: comms owned alone is P5, where chats keep the write-back), and
// nothing once every domain is PostgreSQL-owned.
func TestWriteBackCredentialsFollowComms(t *testing.T) {
	for _, tc := range []struct {
		owned       string
		needsCreds  bool
		withProject bool
	}{
		{owned: "", needsCreds: true, withProject: true},
		{owned: "operations", needsCreds: true, withProject: true},
		{owned: "comms", needsCreds: true, withProject: true},
		{owned: "operations,comms", needsCreds: true, withProject: true},
		{owned: "auth,masterdata,operations,billing,hr,comms,security,mobile_release", needsCreds: true, withProject: true},
		{owned: "all", withProject: true},
		{owned: "auth", withProject: false},
	} {
		extra := []string{"PG_OWNED_DOMAINS=" + tc.owned}
		if tc.withProject {
			extra = append(extra, "WRITEBACK_FIRESTORE_PROJECT_ID=example-writeback")
		}
		_, err := config.LoadFrom[app.APIConfig](override(baseEnv(), extra))
		if tc.needsCreds {
			if err == nil || !strings.Contains(err.Error(), "GOOGLE_APPLICATION_CREDENTIALS") {
				t.Errorf("%q: want the credentials error, got %v", tc.owned, err)
			}
			continue
		}
		if err != nil {
			t.Errorf("%q: %v", tc.owned, err)
		}
	}
}
