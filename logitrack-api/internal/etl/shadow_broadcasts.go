package etl

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/etl/gcp"
)

// The broadcasts projection (T48, issue #68; main spec §12.1 class B, §13.8): from P5 PostgreSQL writes the broadcasts
// (internal/broadcasts) and firestore.shadow copies them to the Firestore broadcasts collection, which the installed
// 3.x APKs stream (broadcast_repository.dart: the newest 100 by sentAt, the badge's latest one) until P7b. Firestore
// has one fleet, the 3.x app lists every document of the collection and the Firestore rules let any signed-in user read
// it, so only what every 3.x user may see is projected: the platform-wide broadcasts (tenant_id NULL) and the own
// fleet's group broadcasts (loadBroadcast reads every document as platform-wide). A carrier's broadcast is not (Skip:
// its drivers get the push, and their 4.x app lists it), nor a broadcast to selected users (group selected: the api
// refuses such a send while 3.x APKs may be recipients, broadcasts.Deps.LegacyFleet, so one exists only from P7b; a
// P7b rollback must not show it to every 3.x user). A broadcast voided in PostgreSQL (the soft delete of DELETE
// /v1/broadcasts/{id}, R14) disappears from the projection: its document is deleted (ExportDoc.Absent), as the legacy
// web deleted it (BroadcastComposer.tsx:178), while the row stays; a projection that wrote a document while the void
// committed deletes it again (voidedBroadcastDocs, run after every write), so a send racing its void leaves no
// document behind.
//
// The read receipts are PostgreSQL's from P5 (the markBroadcastRead shim writes broadcast_reads), and broadcast.read
// projects them: readDriverAuthIds and readCount are what the legacy web history and composer show
// (BroadcastHistoryModal.tsx:115, BroadcastComposer.tsx:155) and what the pre-shim callable checks (alreadyRead,
// fn:chat.ts:241-249), the readers of a P5 rollback. Both only ever grow, as the legacy arrayUnion and increment made
// them: readDriverAuthIds keeps every stored reader and adds the Firebase uids of PostgreSQL's (a reader the load could
// not resolve, or one the legacy callable added meanwhile, is never removed), and readCount counts PostgreSQL's
// readers plus the stored ones PostgreSQL lacks, never less than the stored count.

// encodeBroadcasts: broadcasts -> broadcasts/{id} (§A.3.10 inverse, loadBroadcast): createdBy (the sender's Firebase
// uid when it has one), createdByName, title, messageText, recipientCount, recipientGroup, sentAt, readCount,
// readDriverAuthIds. A broadcast born in PostgreSQL mints its document id (id::text) when it is first projected; a
// voided one is never minted and its document, if any, is deleted.
func encodeBroadcasts(ctx context.Context, tx pgx.Tx, sel selection, mint bool) ([]ExportDoc, error) {
	rows, err := tx.Query(ctx, `SELECT b.id, b.legacy_doc_id, (b.tenant_id IS NULL OR t.kind = 'own_fleet') AS projectable,
		  b.voided_at IS NOT NULL AS voided, u.legacy_auth_uid, b.created_by_name, b.title, b.message_text, b.recipient_group,
		  b.recipient_count, b.sent_at,
		  (SELECT count(*) FROM broadcast_reads r WHERE r.broadcast_id = b.id) AS reads,
		  coalesce((SELECT array_agg(ru.legacy_auth_uid ORDER BY ru.legacy_auth_uid COLLATE "C") FROM broadcast_reads r
		    JOIN users ru ON ru.id = r.user_id WHERE r.broadcast_id = b.id AND ru.legacy_auth_uid IS NOT NULL), '{}') AS readers
		FROM broadcasts b LEFT JOIN tenants t ON t.id = b.tenant_id LEFT JOIN users u ON u.id = b.created_by_user_id
		WHERE $1 OR b.id = ANY($2) ORDER BY b.id`, sel.all, sel.ids)
	if err != nil {
		return nil, fmt.Errorf("etl: encode broadcasts: %w", err)
	}
	type broadcast struct {
		id                      uuid.UUID
		legacy, by, name, title *string
		projectable, voided     bool
		text, group             string
		count                   int32
		sent                    time.Time
		reads                   int64
		readers                 []string
	}
	var bs []broadcast
	for rows.Next() {
		var b broadcast
		if err := rows.Scan(&b.id, &b.legacy, &b.projectable, &b.voided, &b.by, &b.name, &b.title, &b.text, &b.group,
			&b.count, &b.sent, &b.reads, &b.readers); err != nil {
			rows.Close()
			return nil, err
		}
		bs = append(bs, b)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	var out []ExportDoc
	for _, b := range bs {
		path := "broadcasts/" + deref(b.legacy, b.id.String())
		switch {
		case !b.projectable:
			out = append(out, newDoc(path).skip("a carrier's broadcast: Firestore holds the own fleet's and the platform-wide ones").doc())
			continue
		case b.voided:
			out = append(out, ExportDoc{Path: path, Absent: true})
			continue
		case b.group == broadcastGroupSelected:
			out = append(out, newDoc(path).skip("a broadcast to selected users: the 3.x app lists every document").doc())
			continue
		}
		docID, err := mintBroadcastID(ctx, tx, b.id, b.legacy, mint)
		if err != nil {
			return nil, err
		}
		count := int64(b.count)
		d := newDoc("broadcasts/"+docID).
			uid("createdBy", b.by).
			set("createdByName", b.name).
			set("title", b.title).
			set("messageText", b.text).
			set("recipientCount", count).eq("recipientCount", sameCount(count)).
			set("recipientGroup", b.group).eq("recipientGroup", sameGroup(b.group)).
			set("sentAt", b.sent).
			set("readDriverAuthIds", nil).
			set("readCount", nil)
		d.d.Merge["readDriverAuthIds"] = mergeReaders(b.readers)
		d.d.Merge["readCount"] = mergeReadCount(b.readers, b.reads)
		out = append(out, d.doc())
	}
	return out, nil
}

// broadcastGroupSelected is broadcasts.GroupSelected (recipientUserIds; etl does not import the api's packages).
const broadcastGroupSelected = "selected"

// storedStrings are the string entries of a stored array.
func storedStrings(v any) []string {
	list, _ := v.([]any)
	out := make([]string, 0, len(list))
	for _, x := range list {
		if s, ok := x.(string); ok && s != "" {
			out = append(out, s)
		}
	}
	return out
}

// mergeReaders: readDriverAuthIds as stored (every entry, in its order) plus the Firebase uids of PostgreSQL's readers
// it lacks (pg, sorted); nil while there is none, so an absent list stays absent.
func mergeReaders(pg []string) func(cur any, _ map[string]any) any {
	return func(cur any, _ map[string]any) any {
		stored, _ := cur.([]any)
		out := slices.Clone(stored)
		have := storedStrings(cur)
		for _, uid := range pg {
			if !slices.Contains(have, uid) {
				out = append(out, uid)
			}
		}
		if len(out) == 0 {
			return nil
		}
		return out
	}
}

// mergeReadCount: PostgreSQL's readers (reads, uids the Firebase uids among them) plus the stored readers PostgreSQL
// lacks, never below the stored readCount (reads only grow; a legacy count above its list stays). No reader and no
// stored count leaves the field absent: the legacy readers take an absent count as 0 (readCount ?? 0, and the
// callable's increment starts from it).
func mergeReadCount(uids []string, reads int64) func(cur any, doc map[string]any) any {
	return func(cur any, doc map[string]any) any {
		n := reads
		var seen []string
		for _, uid := range storedStrings(doc["readDriverAuthIds"]) {
			if !slices.Contains(uids, uid) && !slices.Contains(seen, uid) {
				seen = append(seen, uid)
				n++
			}
		}
		c, ok := toFloat(cur)
		switch {
		case ok && int64(c) > n:
			n = int64(c)
		case !ok && n == 0:
			return nil
		}
		return n
	}
}

// sameCount: a stored number of the value of want; an absent count is the 0 the loader reads.
func sameCount(want int64) func(cur any, has bool) bool {
	return func(cur any, has bool) bool {
		if !has {
			return want == 0
		}
		return sameNumber(float64(want))(cur, has)
	}
}

// sameGroup: the stored recipient group; an absent or blank one is the column default all_driver the loader keeps.
func sameGroup(want string) func(cur any, has bool) bool {
	return func(cur any, has bool) bool {
		s, ok := cur.(string)
		if !has || !ok || strings.TrimSpace(s) == "" {
			return want == "all_driver"
		}
		return s == want
	}
}

// mintBroadcastID is mintID for broadcasts, which have no updated_at (the row is written once, then only voided).
func mintBroadcastID(ctx context.Context, tx pgx.Tx, id uuid.UUID, legacy *string, mint bool) (string, error) {
	if legacy != nil && *legacy != "" {
		return *legacy, nil
	}
	docID := id.String()
	if mint {
		if _, err := tx.Exec(ctx, `UPDATE broadcasts SET legacy_doc_id = $2 WHERE id = $1 AND legacy_doc_id IS NULL`, id, docID); err != nil {
			return "", fmt.Errorf("etl: mint the document id of broadcasts %s: %w", id, err)
		}
	}
	return docID, nil
}

// voidedBroadcastDocs are the document paths of the broadcasts among ids that are voided (absentAfterWrite). A row
// never minted has no document: its uuid names the path the projection would have written.
func voidedBroadcastDocs(ctx context.Context, tx pgx.Tx, ids []uuid.UUID) ([]string, error) {
	rows, err := tx.Query(ctx, `SELECT 'broadcasts/' || coalesce(legacy_doc_id, id::text) FROM broadcasts
		WHERE id = ANY($1) AND voided_at IS NOT NULL ORDER BY 1`, ids)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, pgx.RowTo[string])
}

// deleteAbsent deletes the document of a row that must not have one (ExportDoc.Absent: a voided broadcast); it reports
// whether a document was there.
func deleteAbsent(ctx context.Context, fs FirestoreWriter, path string) (bool, error) {
	if _, err := fs.Get(ctx, path); errors.Is(err, gcp.ErrNotFound) {
		return false, nil
	} else if err != nil {
		return false, fmt.Errorf("etl: project %s: %w", path, err)
	}
	if err := fs.Delete(ctx, path, gcp.Precondition{}); err != nil {
		return false, fmt.Errorf("etl: project %s: delete: %w", path, err)
	}
	return true, nil
}
