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
// wb (buildWriteBacks: Firestore first through the write-back project and port of T31, WRITEBACK_FIRESTORE_PROJECT_ID
// and writeback.Firestore, the FS->PG mirror, cache:mirror_ack); without that project (wb nil) they answer 503
// write_back_unconfigured and PostgreSQL is never written in Firestore's place (§12.1). The reads serve PostgreSQL
// either way. roles (iam.RBAC) decides who may be assigned a chat.
func buildChats(cfg *APIConfig, pool db.Beginner, st *storage.Service, wb *chats.WriteBack, roles chats.RoleSets,
	log zerolog.Logger) (*chats.Service, error) {
	owned := CommsOwned(cfg.PGOwnedDomains)
	if owned {
		log.Info().Msg("chat writes: PostgreSQL (comms is in PG_OWNED_DOMAINS)")
		wb = nil
	}
	return chats.New(chats.Deps{Pool: pool, Files: st, Roles: roles, PGOwned: owned, WriteBack: wb, Log: log})
}
