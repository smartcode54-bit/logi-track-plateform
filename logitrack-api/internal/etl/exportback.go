package etl

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/etl/dump"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/etl/gcp"
)

// Export-back (main spec §13.10): the rollback of a class A domain writes its PostgreSQL rows back to Firestore in
// the legacy shape, doc id = legacy_doc_id (id::text for a row born in PostgreSQL, §13.8). A document whose
// Firestore UpdateTime is newer than the freeze was changed after the cut-over: it is refused unless the operator
// reruns with --overwrite-after-freeze. Every write is field-masked to the fields the encoder owns (a NULL column
// deletes its field) and carries a precondition on the UpdateTime it was compared with, so a concurrent writer is
// never overwritten silently.

// FirestoreWriter is the part of the Firestore client export-back needs (gcp.Firestore).
type FirestoreWriter interface {
	Get(ctx context.Context, path string) (dump.Doc, error)
	Patch(ctx context.Context, path string, fields map[string]any, mask []string, pre gcp.Precondition) (dump.Doc, error)
}

// ExportDoc is one encoded legacy document (export-back and the firestore.shadow projection share the encoders).
type ExportDoc struct {
	Path   string
	Fields map[string]any
	Mask   []string // the fields the encoder owns
	// Equiv decides, per field, whether a stored value means what PostgreSQL holds (cur, and whether the field is
	// stored at all), so the projection keeps a legacy spelling instead of rewriting it (Diff).
	Equiv map[string]func(cur any, has bool) bool
	// Keep names, from the whole stored document, the fields the projection leaves as they are: a value PostgreSQL
	// holds no row for because the load could not resolve it (an unknown linked party, a duplicate GPS id or carrier
	// code, an assignment that did not load) stays in Firestore instead of being deleted.
	Keep func(cur map[string]any) []string
	// Skip, when set, says why the row is not written (a quarantined row, a hub a driver created in Firestore).
	Skip string
	// Merge, per field, builds the value to write from the stored one (nil when the field is absent or the document
	// does not exist): a map whose entries PostgreSQL holds only in part (customerDriverIds keeps the entries of a code
	// the load could not resolve, under the stored spelling of each key). The field is the same when the merged value
	// is legacyEqual to the stored one.
	Merge map[string]func(cur any) any
	// Retired marks the document of a row whose document id moved (a distance pair minted again after its hub's code
	// changed): it is written only when it exists, never created, and the reconciliation does not count it as a row.
	Retired bool
	// Hold, given the whole stored document, names why the projection must leave it alone ("" to write it): the
	// version gate of another flavor's fleet (settings/mobile_app). A held document is not written (counted skipped,
	// logged) and the reconciliation reports it as differing.
	Hold func(cur map[string]any) string
}

// ExportOptions select an export-back run.
type ExportOptions struct {
	Collection           string
	FrozenAt             time.Time // the class A freeze (cut-over) instant; required
	OverwriteAfterFreeze bool
}

// ExportResult lists what a run did.
type ExportResult struct {
	Written []string
	Refused []string // changed in Firestore after the freeze
}

// exportEncoders are the class A collections export-back can write.
var exportEncoders = map[string]func(context.Context, pgx.Tx) ([]ExportDoc, error){
	// The projection's encoder (shadow_encode.go) over every row, without minting: export-back never writes PostgreSQL.
	"customers": func(ctx context.Context, tx pgx.Tx) ([]ExportDoc, error) {
		return encodeCustomerDocs(ctx, tx, selectAll(), false)
	},
}

// ExportCollections lists the collections export-back supports.
func ExportCollections() []string {
	out := make([]string, 0, len(exportEncoders))
	for k := range exportEncoders {
		out = append(out, k)
	}
	slices.Sort(out)
	return out
}

// Encode reads one collection's rows in the legacy shape.
func (e *Engine) Encode(ctx context.Context, collection string) ([]ExportDoc, error) {
	enc, ok := exportEncoders[collection]
	if !ok {
		return nil, fmt.Errorf("etl: export-back supports %v, not %q", ExportCollections(), collection)
	}
	var docs []ExportDoc
	err := e.cfg.Tx(ctx, func(tx pgx.Tx) error {
		var err error
		docs, err = enc(ctx, tx)
		return err
	})
	return docs, err
}

// ExportBack writes one collection back to Firestore.
func (e *Engine) ExportBack(ctx context.Context, w FirestoreWriter, o ExportOptions) (*ExportResult, error) {
	if o.FrozenAt.IsZero() {
		return nil, errors.New("etl: export-back needs the freeze instant (--frozen-at)")
	}
	docs, err := e.Encode(ctx, o.Collection)
	if err != nil {
		return nil, err
	}
	res := &ExportResult{}
	for _, d := range docs {
		if d.Skip != "" {
			continue
		}
		cur, err := w.Get(ctx, d.Path)
		var pre gcp.Precondition
		switch {
		case errors.Is(err, gcp.ErrNotFound):
			no := false
			pre.Exists = &no
		case err != nil:
			return res, fmt.Errorf("etl: export-back %s: %w", d.Path, err)
		case cur.UpdateTime.After(o.FrozenAt) && !o.OverwriteAfterFreeze:
			res.Refused = append(res.Refused, d.Path)
			continue
		default:
			pre.UpdateTime = cur.UpdateTime
		}
		if _, err := w.Patch(ctx, d.Path, d.Fields, d.Mask, pre); err != nil {
			return res, fmt.Errorf("etl: export-back %s: %w", d.Path, err)
		}
		res.Written = append(res.Written, d.Path)
	}
	return res, nil
}
