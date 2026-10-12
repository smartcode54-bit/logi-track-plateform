package app

import "testing"

// The chat writes go to PostgreSQL only while PG_OWNED_DOMAINS holds comms (T47); anything else (empty, other domains,
// an invalid value that config validation refuses anyway) keeps them on the write-back path.
func TestCommsOwned(t *testing.T) {
	for v, want := range map[string]bool{"": false, "all": true, "comms": true, "auth, comms": true, "auth,operations": false,
		"comms,comms": false, "nope": false} {
		if got := CommsOwned(v); got != want {
			t.Errorf("%q: %v", v, got)
		}
	}
}
