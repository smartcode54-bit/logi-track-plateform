package app

import (
	"slices"

	"github.com/rs/zerolog"

	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/chats"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/db"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/storage"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/webcfg"
)

// CommsDomain is the PG_OWNED_DOMAINS entry of the chats and broadcasts (main spec §12.1).
const CommsDomain = "comms"

// CommsOwned reports whether PG_OWNED_DOMAINS holds comms: the chat writes then go to PostgreSQL (T47).
func CommsOwned(pgOwnedDomains string) bool {
	owned, err := webcfg.ParseOwned(pgOwnedDomains)
	return err == nil && slices.Contains(owned, CommsDomain)
}

// buildChats builds the chat service of T47 (/v1/chats*, /v1/mobile/chat*). With comms in PG_OWNED_DOMAINS (the
// staging VM, every environment from P7b) the writes go to PostgreSQL under the caller's RLS with their events.
// Otherwise chats are written in Firestore by the installed 3.x APKs and the chat writes take the class C write-back
// (chats.WriteBack: Firestore first, the FS->PG mirror, cache:mirror_ack); this api has no Firestore writer for it yet
// (the project of the write-back, WRITEBACK_FIRESTORE_PROJECT_ID, arrives with T31), so they answer 503
// write_back_unconfigured and PostgreSQL is never written in Firestore's place (§12.1). The reads serve PostgreSQL
// either way.
func buildChats(cfg *APIConfig, pool db.Beginner, st *storage.Service, log zerolog.Logger) (*chats.Service, error) {
	owned := CommsOwned(cfg.PGOwnedDomains)
	if owned {
		log.Info().Msg("chat writes: PostgreSQL (comms is in PG_OWNED_DOMAINS)")
	} else {
		log.Info().Msg("chat writes off: comms is Firestore-owned and the api has no Firestore writer " +
			"(the chat write routes answer 503 write_back_unconfigured)")
	}
	return chats.New(chats.Deps{Pool: pool, Files: st, PGOwned: owned, Log: log})
}
