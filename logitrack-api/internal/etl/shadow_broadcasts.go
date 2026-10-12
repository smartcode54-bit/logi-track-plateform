package etl

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/etl/gcp"
)

// The broadcasts projection (T48, issue #68; main spec §12.1 class B, §13.8): from P5 PostgreSQL writes the broadcasts
// (internal/broadcasts) and firestore.shadow copies them to the Firestore broadcasts collection, which the installed
// 3.x APKs stream (broadcast_repository.dart: the newest 100 by sentAt, the badge's latest one) until P7b. Firestore
// has one fleet and the 3.x app lists every document of the collection, so the platform-wide broadcasts (tenant_id
// NULL) and the own fleet's are projected (loadBroadcast reads every document as platform-wide), a carrier's are not
// (Skip: its drivers get the push, and their 4.x app lists it). A broadcast voided in PostgreSQL (the soft delete of
// DELETE /v1/broadcasts/{id}, R14) disappears from the projection: its document is deleted (ExportDoc.Absent), as the
// legacy web deleted it (BroadcastComposer.tsx:178), while the row stays. The read receipts are PostgreSQL's from P5
// (the markBroadcastRead shim writes broadcast_reads), so readCount and readDriverAuthIds, which no 3.x reader reads,
// are outside the mask and keep what Firestore holds.

// encodeBroadcasts: broadcasts -> broadcasts/{id} (§A.3.10 inverse, loadBroadcast): createdBy (the sender's Firebase
// uid when it has one), createdByName, title, messageText, recipientCount, recipientGroup, sentAt. A broadcast born in
// PostgreSQL mints its document id (id::text) when it is first projected; a voided one is never minted and its
// document, if any, is deleted.
func encodeBroadcasts(ctx context.Context, tx pgx.Tx, sel selection, mint bool) ([]ExportDoc, error) {
	rows, err := tx.Query(ctx, `SELECT b.id, b.legacy_doc_id, (b.tenant_id IS NULL OR t.kind = 'own_fleet') AS projectable,
		  b.voided_at IS NOT NULL AS voided, u.legacy_auth_uid, b.created_by_name, b.title, b.message_text, b.recipient_group,
		  b.recipient_count, b.sent_at
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
	}
	var bs []broadcast
	for rows.Next() {
		var b broadcast
		if err := rows.Scan(&b.id, &b.legacy, &b.projectable, &b.voided, &b.by, &b.name, &b.title, &b.text, &b.group,
			&b.count, &b.sent); err != nil {
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
			set("sentAt", b.sent)
		out = append(out, d.doc())
	}
	return out, nil
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
