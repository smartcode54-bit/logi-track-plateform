package etl

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"time"

	"github.com/google/uuid"
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

// ExportDoc is one encoded legacy document.
type ExportDoc struct {
	Path   string
	Fields map[string]any
	Mask   []string // the fields the encoder owns
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
	"customers": encodeCustomers,
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

// customerFields are the fields of a customers doc the encoder owns (validate/customerSchema.ts).
var customerFields = []string{"code", "name", "description", "logoUrl", "driverIdTypes", "address", "taxId", "branchType",
	"branchNumber", "contactName", "contactPhone", "billingEmail", "paymentTermsDays", "invoiceNote", "billingDateBasis",
	"lineGroupId", "createdAt", "updatedAt"}

// encodeCustomers: customers + billing_parties + customer_driver_id_types + the logo's legacy URL -> customers/{id}.
// branch_type hq/branch become สำนักงานใหญ่/สาขา again; driverIdTypes is written even when empty (the schema's
// default).
func encodeCustomers(ctx context.Context, tx pgx.Tx) ([]ExportDoc, error) {
	rows, err := tx.Query(ctx, `SELECT c.id, coalesce(c.legacy_doc_id, c.id::text), c.code::text, c.name, c.description, f.legacy_url, c.address,
		  c.tax_id, c.branch_type, c.branch_number, c.contact_name, c.contact_phone, c.billing_email::text, c.payment_terms_days,
		  c.invoice_note, bp.billing_date_basis, c.line_group_id, c.created_at, c.updated_at
		FROM customers c LEFT JOIN billing_parties bp ON bp.customer_id = c.id LEFT JOIN file_objects f ON f.id = c.logo_file_id
		ORDER BY 2`)
	if err != nil {
		return nil, err
	}
	type cust struct {
		id                                                                               uuid.UUID
		docID, code, name                                                                string
		desc, logo, addr, tax, branch, branchNo, cname, cphone, email, note, basis, line *string
		terms                                                                            *int32
		created, updated                                                                 time.Time
	}
	var cs []cust
	for rows.Next() {
		var c cust
		if err := rows.Scan(&c.id, &c.docID, &c.code, &c.name, &c.desc, &c.logo, &c.addr, &c.tax, &c.branch, &c.branchNo, &c.cname,
			&c.cphone, &c.email, &c.terms, &c.note, &c.basis, &c.line, &c.created, &c.updated); err != nil {
			return nil, err
		}
		cs = append(cs, c)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	var out []ExportDoc
	for _, c := range cs {
		f := map[string]any{"code": c.code, "name": c.name,
			"createdAt": dump.Timestamp{Time: c.created.UTC()}, "updatedAt": dump.Timestamp{Time: c.updated.UTC()}}
		put := func(k string, v *string) {
			if v != nil {
				f[k] = *v
			}
		}
		put("description", c.desc)
		put("logoUrl", c.logo)
		put("address", c.addr)
		put("taxId", c.tax)
		put("branchNumber", c.branchNo)
		put("contactName", c.cname)
		put("contactPhone", c.cphone)
		put("billingEmail", c.email)
		put("invoiceNote", c.note)
		put("billingDateBasis", c.basis)
		put("lineGroupId", c.line)
		if c.branch != nil {
			f["branchType"] = map[string]string{"hq": "สำนักงานใหญ่", "branch": "สาขา"}[*c.branch]
		}
		if c.terms != nil {
			f["paymentTermsDays"] = int64(*c.terms)
		}
		types := []any{}
		trows, err := tx.Query(ctx, `SELECT key, label FROM customer_driver_id_types WHERE customer_id = $1 ORDER BY position, key`, c.id)
		if err != nil {
			return nil, err
		}
		for trows.Next() {
			var k, l string
			if err := trows.Scan(&k, &l); err != nil {
				return nil, err
			}
			types = append(types, map[string]any{"key": k, "label": l})
		}
		if err := trows.Err(); err != nil {
			return nil, err
		}
		f["driverIdTypes"] = types
		out = append(out, ExportDoc{Path: "customers/" + c.docID, Fields: f, Mask: customerFields})
	}
	return out, nil
}
