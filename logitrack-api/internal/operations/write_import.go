package operations

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/authz"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/billing/compute"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/fleet/vehicleclass"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/operations/operationsdb"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/httpx"
)

// POST /v1/tasks/import (Appendix B §B.2.11): the board's spreadsheet import (first-mile/import-dialog.tsx:136-355,
// line-haul, job-assign). The browser still reads the sheet and lets the user pick each row's customer; the server
// takes the rows as JSON, validates each one as the dialog did and creates the valid ones with numbers sequenced
// server-side (next_task_seq() per row: no count()+1 per day, R10). Rules per row:
//
//   - date (a Bangkok day yyyy-MM-dd or an RFC 3339 instant), sourceHub, destination and billingPartyId are required;
//   - jobCategory is the cell text read by compute.JobCategoryFromCell (port of lib/jobCategory.ts): blank is
//     PRIMARY, หลัก / เสริม / primary / supplementary in any case, anything else rejects the row (a misspelled "เสิรม"
//     is never priced as PRIMARY);
//   - plate is matched to the fleet (upper case without spaces and dashes, normalizePlate): an unknown plate rejects
//     the row, a blank plate is allowed; a matched truck gives the vehicle class, else truckType must be a class;
//   - taskNo (the sheet's own shipment id) is kept verbatim, else the number is allocated.
//
// The imported tasks are pending (no driver), so nothing is pushed: notify is accepted and must stay false. dryRun
// validates without writing. At most MaxImportRows rows per request (WriteBackBatch on the write-back path, one
// Firestore commit); each row runs in a savepoint, so one row's refusal
// never undoes another. The answer is {created: [{row, id, taskNo}], errors: [{row, fields: [...]}], dryRun}.

// MaxImportRows bounds one import request.
const MaxImportRows = 1000

// importRowFields are the fields of one import row.
var importRowFields = []string{"date", "time", "sourceHub", "destination", "billingPartyId", "jobCategory", "truckType",
	"plate", "taskNo", "note"}

// ImportResult is the answer of POST /v1/tasks/import.
type ImportResult struct {
	Created []ImportedTask `json:"created"`
	Errors  []ImportError  `json:"errors"`
	DryRun  bool           `json:"dryRun"`
	// Pending is set on the 202 of a write-back import the mirror did not acknowledge within the wait.
	Pending bool `json:"pending,omitempty"`
}

// ImportedTask is one created (or, on a dry run, valid) row.
type ImportedTask struct {
	Row    int        `json:"row"`
	ID     *uuid.UUID `json:"id,omitempty"`
	TaskNo *string    `json:"taskNo,omitempty"`
}

// ImportError is one refused row with the reasons (details.fields of the 422 the row alone would have had).
type ImportError struct {
	Row    int                    `json:"row"`
	Code   string                 `json:"code"`
	Fields []httpx.FieldViolation `json:"fields,omitempty"`
}

// plateStrip are the characters a plate key drops: JavaScript's \s (trimChars, the ECMAScript WhiteSpace and
// LineTerminator set: NBSP, the Zs spaces, BOM and the rest, which Go's \s and PostgreSQL's [[:space:]] partly miss)
// and the dash.
const plateStrip = trimChars + "-"

// plateKey is normalizePlate of the import dialog (import-dialog.tsx:18: upper case, /[\s-]/g removed). The fleet
// side is normalised with the same set (TrucksByPlate, translate with plateStrip).
func plateKey(s string) string {
	return strings.ToUpper(strings.Map(func(r rune) rune {
		if strings.ContainsRune(plateStrip, r) {
			return -1
		}
		return r
	}, s))
}

// importRow is one parsed row before its fleet lookup.
type importRow struct {
	index int
	in    taskFields
	plate string
	errs  []httpx.FieldViolation
}

// ImportTasksAPI is POST /v1/tasks/import (operations:manage_tasks).
func (s *Service) ImportTasksAPI(ctx context.Context, p *authz.Principal, rid string, body []byte) (WriteResult, error) {
	if err := requireCap(p, authz.OperationsManageTasks); err != nil {
		return WriteResult{}, err
	}
	mode, err := s.mode()
	if err != nil {
		return WriteResult{}, err
	}
	var ck checker
	f, err := parseFields(body, &ck)
	if err != nil {
		return WriteResult{}, err
	}
	f.only("taskType", "rows", "notify", "dryRun")
	taskType, ok := taskTypeOf(f.required("taskType"))
	if !ok && !ck.hasField("taskType") {
		ck.add("taskType", "invalid")
	}
	if n := f.boolean("notify"); n != nil && *n {
		ck.add("notify", "not_supported") // imported tasks have no driver: there is nobody to push to
	}
	dry := false
	if b := f.boolean("dryRun"); b != nil {
		dry = *b
	}
	var raws []json.RawMessage
	if raw := f.raw("rows"); raw == nil || isNull(raw) || json.Unmarshal(raw, &raws) != nil {
		ck.add("rows", "required")
	}
	limit := MaxImportRows
	if mode == modeWriteBack {
		limit = WriteBackBatch // one Firestore commit: all of the import or none of it
	}
	switch {
	case len(raws) == 0 && !ck.hasField("rows"):
		ck.add("rows", "required")
	case len(raws) > limit:
		ck.bad = append(ck.bad, httpx.FieldViolation{Field: "rows", Reason: "too_many", Params: map[string]any{"max": limit}})
	}
	if err := ck.err(); err != nil {
		return WriteResult{}, err
	}
	rows := make([]importRow, 0, len(raws))
	for i, raw := range raws {
		rows = append(rows, parseImportRow(i+1, taskType, raw))
	}
	d, err := s.directory(ctx, p)
	if err != nil {
		return WriteResult{}, err
	}
	out := ImportResult{Created: []ImportedTask{}, Errors: []ImportError{}, DryRun: dry}
	if mode == modeWriteBack {
		return s.importWriteBack(ctx, p, rid, d, rows, out)
	}
	errDry := errors.New("dry run")
	err = s.Tx(ctx, p, func(tx pgx.Tx) error {
		for _, r := range rows {
			if len(r.errs) > 0 {
				out.Errors = append(out.Errors, ImportError{Row: r.index, Code: httpx.CodeInvalidArgument, Fields: r.errs})
				continue
			}
			sp, err := tx.Begin(ctx)
			if err != nil {
				return err
			}
			task, err := s.importOne(ctx, sp, p, rid, d, r)
			if err != nil {
				_ = sp.Rollback(ctx)
				var he *httpx.Error
				if !errors.As(err, &he) {
					return err
				}
				out.Errors = append(out.Errors, rowError(r.index, he))
				continue
			}
			if err := sp.Commit(ctx); err != nil {
				return err
			}
			id := task.ID
			out.Created = append(out.Created, ImportedTask{Row: r.index, ID: &id, TaskNo: task.TaskNo})
		}
		if dry {
			return errDry
		}
		return nil
	})
	if err != nil && !errors.Is(err, errDry) {
		return WriteResult{}, err
	}
	if dry {
		for i := range out.Created {
			out.Created[i].ID, out.Created[i].TaskNo = nil, nil // nothing was written: no id, no number
		}
	}
	return WriteResult{Status: http.StatusOK, Data: out}, nil
}

// rowError turns a row's refusal into its entry of errors[].
func rowError(row int, he *httpx.Error) ImportError {
	e := ImportError{Row: row, Code: he.Code}
	if fs, ok := he.Details["fields"].([]httpx.FieldViolation); ok {
		e.Fields = fs
	} else if field, ok := he.Details["field"].(string); ok {
		reason, _ := he.Details["reason"].(string)
		if reason == "" {
			reason = he.Code
		}
		e.Fields = []httpx.FieldViolation{{Field: field, Reason: reason}}
	}
	return e
}

// parseImportRow reads one row; the violations stay on the row (the import goes on).
func parseImportRow(index int, taskType string, raw json.RawMessage) importRow {
	r := importRow{index: index}
	var ck checker
	if !bytes.HasPrefix(bytes.TrimSpace(raw), []byte("{")) {
		ck.add("row", "invalid")
		r.errs = ck.bad
		return r
	}
	f, err := parseFields(raw, &ck)
	if err != nil {
		ck.add("row", "invalid")
		r.errs = ck.bad
		return r
	}
	f.only(importRowFields...)
	in := taskFields{set: map[string]bool{}, taskType: taskType}
	in.planAt = f.planInstant("date")
	if !f.has("date") {
		ck.add("date", "required")
	}
	if v := f.required("sourceHub"); v != "" {
		in.sourceHub = &v
	}
	if v := f.required("destination"); v != "" {
		in.destination = &v
	}
	if in.billingPartyID = f.id("billingPartyId"); in.billingPartyID == nil && !ck.hasField("billingPartyId") {
		ck.add("billingPartyId", "required")
	}
	in.planTime = f.planTime("time")
	cell := ""
	if v := f.text("jobCategory"); v != nil {
		cell = *v
	}
	if c, ok := compute.JobCategoryFromCell(cell); ok {
		jc := string(c)
		in.jobCategory = &jc
	} else {
		ck.add("jobCategory", "invalid")
	}
	if v := f.optText("plate"); v != nil {
		r.plate = plateKey(*v)
	}
	if v := f.optText("truckType"); v != nil && r.plate == "" {
		if up := strings.ToUpper(*v); vehicleclass.Valid(up) {
			in.truckType = &up
		} else {
			ck.add("truckType", "invalid")
		}
	}
	in.taskNo = f.optText("taskNo")
	if in.taskNo != nil {
		// A sheet's canonical number moves the global counter of the type and day it names (CreateTask): it must be a
		// number of this import's type and of its row's plan day, and within the allocator's three digits (T31 review).
		// Any other text (a shipment id) is kept verbatim and moves nothing.
		if typ, day, seq, padded, ok := ParseTaskNo(*in.taskNo); ok && padded {
			switch {
			case typ != taskType || (in.planAt != nil && !day.Equal(PlanDate(*in.planAt))):
				ck.add("taskNo", "mismatch")
			case seq > MaxSheetTaskSeq:
				ck.add("taskNo", "out_of_range")
			}
		}
	}
	for _, k := range []string{"time", "jobCategory"} {
		if f.has(k) {
			in.set[k] = true
		}
	}
	r.in, r.errs = in, ck.bad
	return r
}

// importOne creates one valid row in its savepoint: the plate's truck (unknown: plate not_in_fleet; two trucks of one
// plate: plate ambiguous), then the same path as POST /v1/tasks.
func (s *Service) importOne(ctx context.Context, tx pgx.Tx, p *authz.Principal, rid string, d directory, r importRow) (Task, error) {
	if err := s.importTruck(ctx, operationsdb.New(tx), &r); err != nil {
		return Task{}, err
	}
	return s.insertTask(ctx, tx, p, rid, d, r.in)
}

// importTruck resolves the plate of a row to its truck (the truck's class wins over the truckType cell).
func (s *Service) importTruck(ctx context.Context, q *operationsdb.Queries, r *importRow) error {
	if r.plate == "" {
		return nil
	}
	trucks, err := q.TrucksByPlate(ctx, operationsdb.TrucksByPlateParams{StripChars: plateStrip, PlateKey: r.plate})
	if err != nil {
		return MapError(err)
	}
	switch len(trucks) {
	case 0:
		return httpx.ErrInvalidArgument(violation("plate", "not_in_fleet"))
	case 2:
		return httpx.ErrInvalidArgument(violation("plate", "ambiguous"))
	}
	id := trucks[0].ID
	r.in.truckID = &id
	r.in.set["truckId"] = true
	return nil
}

// importWriteBack creates the valid rows' task documents while operations is Firestore-owned: every row is planned
// and numbered in one transaction of the caller (a refused row allocates no number), then the documents are created in
// one Firestore commit (at most WriteBackBatch rows, checked before anything is planned: the import is all or nothing,
// so a failed commit leaves no half of it to be imported twice), and the answer waits for the mirror's
// acknowledgement of the first document (202 otherwise).
func (s *Service) importWriteBack(ctx context.Context, p *authz.Principal, _ string, d directory, rows []importRow, out ImportResult) (WriteResult, error) {
	var docs []wbDoc
	errDry := errors.New("dry run")
	err := s.Tx(ctx, p, func(tx pgx.Tx) error {
		for _, r := range rows {
			if len(r.errs) > 0 {
				out.Errors = append(out.Errors, ImportError{Row: r.index, Code: httpx.CodeInvalidArgument, Fields: r.errs})
				continue
			}
			sp, err := tx.Begin(ctx)
			if err != nil {
				return err
			}
			doc, task, err := s.planImportWriteBack(ctx, sp, operationsdb.New(sp), p, d, r)
			if err != nil {
				_ = sp.Rollback(ctx)
				var he *httpx.Error
				if !errors.As(err, &he) {
					return err
				}
				out.Errors = append(out.Errors, rowError(r.index, he))
				continue
			}
			if err := sp.Commit(ctx); err != nil {
				return err
			}
			id := task.ID
			out.Created = append(out.Created, ImportedTask{Row: r.index, ID: &id, TaskNo: task.TaskNo})
			docs = append(docs, wbDoc{collection: CollTasks, docID: id.String(), fields: doc, create: true})
		}
		if out.DryRun {
			return errDry
		}
		return nil
	})
	if err != nil && !errors.Is(err, errDry) {
		return WriteResult{}, err
	}
	if out.DryRun || len(docs) == 0 {
		if out.DryRun {
			for i := range out.Created {
				out.Created[i].ID, out.Created[i].TaskNo = nil, nil
			}
		}
		return WriteResult{Status: http.StatusOK, Data: out}, nil
	}
	_, acked, err := s.writeBack(ctx, docs...)
	if err != nil {
		return WriteResult{}, err
	}
	if !acked {
		out.Pending = true
		return WriteResult{Status: http.StatusAccepted, Data: out}, nil
	}
	return WriteResult{Status: http.StatusOK, Data: out}, nil
}

// WriteBackBatch is the most rows a write-back import takes: its documents go in one Firestore commit (a commit takes
// 500 writes), so a failed commit writes none of them (422 rows too_many with details.max above it).
const WriteBackBatch = 400

// planImportWriteBack plans, stamps and numbers one row of a write-back import and encodes its document.
func (s *Service) planImportWriteBack(ctx context.Context, tx pgx.Tx, q *operationsdb.Queries, p *authz.Principal, d directory, r importRow) (map[string]any, Task, error) {
	if err := s.importTruck(ctx, q, &r); err != nil {
		return nil, Task{}, err
	}
	var ck checker
	pl, err := s.planTask(ctx, q, d, &ck, Task{}, r.in, true)
	if err != nil {
		return nil, Task{}, err
	}
	if err := ck.err(); err != nil {
		return nil, Task{}, err
	}
	res, err := Stamp(ctx, tx, p, TableTasks, Links{DriverID: pl.next.DriverID})
	if err != nil {
		return nil, Task{}, err
	}
	id, err := uuid.NewV7()
	if err != nil {
		return nil, Task{}, err
	}
	pl.next.ID, pl.next.TenantID = id, uuid.MustParse(res.TenantID)
	if err := s.checkTenantLinks(ctx, q, p, pl.next.TenantID, taskLinks(pl, Task{}, true)...); err != nil {
		return nil, Task{}, err
	}
	no := r.in.taskNo
	if no == nil {
		day := PlanDate(pl.next.PlanAt)
		seq, err := q.NextTaskSeq(ctx, operationsdb.NextTaskSeqParams{TaskType: pl.next.TaskType, PlanDate: day})
		if err != nil {
			return nil, Task{}, seqError(err)
		}
		no = ptr(FormatTaskNo(pl.next.TaskType, day, int(seq)))
	}
	if typ, day, seq, padded, ok := ParseTaskNo(*no); ok && padded && r.in.taskNo != nil && seq <= MaxBumpSeq {
		// a sheet's canonical number moves its day's counter past it, as CreateTask does (the mirror keeps it verbatim)
		if _, err := q.BumpTaskSeq(ctx, operationsdb.BumpTaskSeqParams{TaskType: typ, PlanDate: day, Seq: int32(seq)}); err != nil {
			return nil, Task{}, seqError(err)
		}
	}
	pl.next.TaskNo = no
	doc, err := encodeTask(pl, true, s.now().UTC())
	return doc, pl.next, err
}
