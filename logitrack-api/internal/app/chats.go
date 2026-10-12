package app

import (
	"github.com/rs/zerolog"

	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/chats"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/db"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/storage"
)

// buildChats builds the chat service of T47 (/v1/chats*, /v1/mobile/chat*). Chats are class C: the installed 3.x APKs
// write them in Firestore until P7b, so the writes go to PostgreSQL (under the caller's RLS, with their events) only
// once every domain is PostgreSQL-owned (classCPGOwned: PG_OWNED_DOMAINS=all, the staging VM and every environment
// from P7b), like the leave decisions of T45. comms in PG_OWNED_DOMAINS alone moves only the broadcasts (class B, T48;
// main spec §12.2 P5: chats keep Firestore as their writer with the admin write-back). Before that the chat writes take
// the class C write-back wb (buildWriteBacks: Firestore first through the write-back project and port of T31,
// WRITEBACK_FIRESTORE_PROJECT_ID and writeback.Firestore, the FS->PG mirror, cache:mirror_ack); without that project
// (wb nil) they answer 503 write_back_unconfigured and PostgreSQL is never written in Firestore's place (§12.1). The
// reads serve PostgreSQL either way. roles (iam.RBAC) decides who may be assigned a chat.
func buildChats(cfg *APIConfig, pool db.Beginner, st *storage.Service, wb *chats.WriteBack, roles chats.RoleSets,
	log zerolog.Logger) (*chats.Service, error) {
	owned := classCPGOwned(cfg.PGOwnedDomains)
	if owned {
		log.Info().Msg("chat writes: PostgreSQL (PG_OWNED_DOMAINS holds every domain)")
		wb = nil
	}
	return chats.New(chats.Deps{Pool: pool, Files: st, Roles: roles, PGOwned: owned, WriteBack: wb, Log: log})
}
