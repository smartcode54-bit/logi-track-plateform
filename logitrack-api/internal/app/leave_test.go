package app

import "testing"

// Leave decisions write PostgreSQL only once every domain is PostgreSQL-owned (T45): hr alone is not enough, since the
// 3.x APKs keep writing leave requests in Firestore until P7b.
func TestLeavePGOwned(t *testing.T) {
	for v, want := range map[string]bool{
		"all": true,
		"auth,masterdata,operations,billing,hr,comms,security,mobile_release,dashboard": true,
		"dashboard, mobile_release,security,comms,hr,billing,operations,masterdata,auth": true,
		"auth,masterdata,billing,hr": false,
		"hr":                         false,
		"":                           false,
		"all,hr":                     false, // invalid: refused by the config, never all-owned
	} {
		if got := leavePGOwned(v); got != want {
			t.Errorf("leavePGOwned(%q) = %v, want %v", v, got, want)
		}
	}
}
