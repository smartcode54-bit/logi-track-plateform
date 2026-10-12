package seed

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/db"
)

// Mode is --mode: insert (the default, after --reset) or upsert (top up a shared database: missing rows only,
// never an UPDATE of an existing row).
type Mode string

// The two load modes.
const (
	ModeInsert Mode = "insert"
	ModeUpsert Mode = "upsert"
)

// LoadResult reports a load.
type LoadResult struct {
	Inserted map[string]int // rows written per table (upsert skips existing ones)
	Counters int            // task_number_counters rows written
	// TemporaryUserInserted reports that this load inserted the must-change-password fixture user, so the
	// temporary password of the plan is the one stored (an upsert that finds the user keeps its password).
	TemporaryUserInserted bool
	// HeldRuns are the plan's payroll run keys an upsert found held by a run the application wrote; the HR rows of
	// their drivers were skipped (SkippedHRDrivers drivers, SkippedHRRows rows: heldHRChains).
	HeldRuns         []HeldRun
	SkippedHRDrivers int
	SkippedHRRows    int
	Duration         time.Duration
}

// HeldRun is a payroll run key of the plan, (driver, pay period, round), that the database holds under another id:
// a run the application wrote (POST /v1/jobs/payroll.run gives a uuidv7), for example a "Generate payroll" on the
// shared database before its demo top-up. The unique key payroll_runs_driver_period keeps one run per key, so the
// seed's run is never written and its children (lines, penalty applications, the payout ledger row, a deducted
// advance) would point at nothing: an upsert keeps the application's run and skips the plan's whole HR chain of that
// driver (heldHRChains), whose penalty balances assume the seed's approvals.
type HeldRun struct {
	DriverID, Period, Round string
	ExistingID, PlanID      string
}

// column is what the loader needs to know about one column.
type column struct {
	typ       string // format_type(atttypid, atttypmod)
	array     bool
	json      bool
	generated bool
	identity  bool
	def       string // pg_get_expr of the default
}

// introspect reads the columns of every table in tables.
func introspect(ctx context.Context, q pgx.Tx, tables []string) (map[string]map[string]column, error) {
	rows, err := q.Query(ctx, `SELECT c.relname::text, a.attname::text, format_type(a.atttypid, a.atttypmod),
		t.typcategory = 'A', t.typname IN ('json','jsonb'), a.attgenerated <> '', a.attidentity <> '',
		coalesce(pg_get_expr(d.adbin, d.adrelid), '')
		FROM pg_attribute a
		JOIN pg_class c ON c.oid = a.attrelid
		JOIN pg_namespace n ON n.oid = c.relnamespace AND n.nspname = 'public'
		JOIN pg_type t ON t.oid = a.atttypid
		LEFT JOIN pg_attrdef d ON d.adrelid = a.attrelid AND d.adnum = a.attnum
		WHERE c.relname = ANY($1::text[]) AND c.relkind = 'r' AND a.attnum > 0 AND NOT a.attisdropped`, tables)
	if err != nil {
		return nil, err
	}
	out := map[string]map[string]column{}
	err = scanAll(rows, func(r pgx.Rows) error {
		var table, name string
		var c column
		if err := r.Scan(&table, &name, &c.typ, &c.array, &c.json, &c.generated, &c.identity, &c.def); err != nil {
			return err
		}
		if out[table] == nil {
			out[table] = map[string]column{}
		}
		out[table][name] = c
		return nil
	})
	if err != nil {
		return nil, err
	}
	for _, t := range tables {
		if out[t] == nil {
			return nil, fmt.Errorf("seed: table %s does not exist: run make migrate first", t)
		}
	}
	return out, nil
}

// clockDefault reports a default that reads the clock: an omitted column with one gets the row's created_at
// so two runs write the same value (§D.4.1).
func clockDefault(def string) bool {
	d := strings.ToLower(def)
	return strings.Contains(d, "now()") || strings.Contains(d, "current_timestamp") ||
		strings.Contains(d, "statement_timestamp()") || strings.Contains(d, "transaction_timestamp()") ||
		strings.Contains(d, "clock_timestamp()")
}

// randomDefault reports a default that differs on every run.
func randomDefault(def string) bool {
	d := strings.ToLower(def)
	return strings.Contains(d, "uuidv7()") || strings.Contains(d, "gen_random_uuid()") || strings.Contains(d, "random()")
}

// param turns one value into an argument and its placeholder: text cast to the column type, text[] for
// arrays, bytea for bytes, JSON text for json and jsonb.
func param(v any, c column, n int) (any, string, error) {
	cast := fmt.Sprintf("$%d::text::%s", n, c.typ)
	if v == nil {
		return nil, cast, nil
	}
	if c.json {
		b, err := json.Marshal(v)
		if err != nil {
			return nil, "", err
		}
		return string(b), cast, nil
	}
	switch x := v.(type) {
	case bool:
		return strconv.FormatBool(x), cast, nil
	case json.Number:
		return x.String(), cast, nil
	case string:
		if x == unresolved {
			return nil, "", errors.New("a computed value was not resolved (the plan is not materialized)")
		}
		return x, cast, nil
	case []byte:
		return x, fmt.Sprintf("$%d::bytea", n), nil
	case []any:
		if !c.array {
			return nil, "", fmt.Errorf("an array for the %s column", c.typ)
		}
		out := make([]string, len(x))
		for i, e := range x {
			switch s := e.(type) {
			case string:
				out[i] = s
			case json.Number:
				out[i] = s.String()
			default:
				return nil, "", fmt.Errorf("array element of type %T", e)
			}
		}
		return out, fmt.Sprintf("$%d::text[]::%s", n, c.typ), nil
	}
	return nil, "", fmt.Errorf("unsupported value of type %T", v)
}

// insertSQL builds the INSERT of one row and its arguments.
func insertSQL(table string, r Row, cols map[string]column, mode Mode) (string, []any, error) {
	names := r.Columns()
	ph := make([]string, 0, len(names))
	args := make([]any, 0, len(names))
	idents := make([]string, 0, len(names))
	for _, name := range names {
		c, ok := cols[name]
		if !ok {
			return "", nil, fmt.Errorf("%s has no column %s", table, name)
		}
		if c.generated || c.identity {
			return "", nil, fmt.Errorf("%s.%s is generated by the database and never written", table, name)
		}
		arg, p, err := param(r[name], c, len(args)+1)
		if err != nil {
			return "", nil, fmt.Errorf("%s.%s: %w", table, name, err)
		}
		args = append(args, arg)
		ph = append(ph, p)
		idents = append(idents, pgx.Identifier{name}.Sanitize())
	}
	sql := "INSERT INTO " + pgx.Identifier{table}.Sanitize() + " (" + strings.Join(idents, ", ") + ") VALUES (" +
		strings.Join(ph, ", ") + ")"
	if mode == ModeUpsert {
		sql += " ON CONFLICT DO NOTHING"
	}
	return sql, args, nil
}

// prepare completes a row for insert: the clock defaults it omits get its created_at, a random default it
// omits is refused, and the deferred columns are split off for the back-fill.
func prepare(table string, r Row, cols map[string]column) (Row, Row, error) {
	row := r.clone()
	created := row["created_at"]
	if created == nil {
		created = fallbackCreatedAt.Format(time.RFC3339)
	}
	for name, c := range cols {
		if _, ok := row[name]; ok || c.generated || c.identity || c.def == "" {
			continue
		}
		if _, deferred := Deferred[table][name]; deferred {
			continue
		}
		switch {
		case clockDefault(c.def) && strings.HasPrefix(c.typ, "timestamp"):
			row[name] = created
		case randomDefault(c.def):
			return nil, nil, fmt.Errorf("%s omits %s, whose default (%s) differs on every run", table, name, c.def)
		}
	}
	var later Row
	for name, initial := range Deferred[table] {
		v, ok := row[name]
		if !ok {
			continue
		}
		if v != nil {
			if later == nil {
				later = Row{}
			}
			later[name] = v
		}
		if initial == nil {
			delete(row, name)
		} else {
			row[name] = initial
		}
	}
	return row, later, nil
}

// Load writes the plan through the ETL login (logitrack_etl, R87) in one db.WithSystem transaction: the
// tables in load order, then the back-filled forward references, the derived task_number_counters and the
// engine check over the priced rows this load inserted (every seeded one in insert mode), so a price the
// engine does not reproduce rolls everything back (Appendix D §D.1.4, §D.1.5). An upsert leaves rows that
// already exist alone, prices included: they are not this load's output, and the application never
// re-prices a frozen or manually edited snapshot either. The generated operations rows an upsert writes are
// priced from the database's pricing inputs once the plan's own are in (repricedTables), so a shared database
// whose rate tables, fees, standby rates, billing basis or hubs differ from the plan's still takes the top-up.
func Load(ctx context.Context, etl db.Beginner, p *Plan, mode Mode) (LoadResult, error) {
	start := time.Now()
	res := LoadResult{Inserted: map[string]int{}}
	err := db.WithSystem(ctx, etl, nil, func(tx pgx.Tx) error {
		if err := sameNamespace(ctx, tx, p); err != nil {
			return err
		}
		cols, err := introspect(ctx, tx, append(slices.Clone(LoadOrder), DerivedTable))
		if err != nil {
			return err
		}
		type backfill struct {
			table string
			id    string
			set   Row
		}
		var later []backfill
		inserted := map[string]map[string]bool{} // table -> keys (rowKey) written by this load
		var chains hrChains
		if mode == ModeUpsert {
			if res.HeldRuns, err = heldRuns(ctx, tx, p.Tables["payroll_runs"]); err != nil {
				return fmt.Errorf("seed: payroll runs of the database: %w", err)
			}
			chains = heldHRChains(p.Tables, res.HeldRuns)
			res.SkippedHRDrivers = len(chains.drivers)
		}
		tables, repriced := p.Tables, false
		for _, table := range LoadOrder {
			if mode == ModeUpsert && !repriced && pricedTables[table] && p.ops != nil {
				// Every pricing input is in by now (TestPricingInputsLoadFirst): the database's rows, where an id or
				// alias the plan also holds kept the database's version (ON CONFLICT DO NOTHING), and the plan's rows
				// for the rest.
				in, err := loadEngineInputs(ctx, tx)
				if err != nil {
					return fmt.Errorf("seed: engine inputs: %w", err)
				}
				if tables, err = repricedTables(p, in); err != nil {
					return err
				}
				repriced = true
			}
			rows := tables[table]
			if mode == ModeUpsert {
				kept := chains.keep(table, rows)
				res.SkippedHRRows += len(rows) - len(kept)
				rows = keepWritable(table, kept, inserted)
			}
			if len(rows) == 0 {
				continue
			}
			batch := &pgx.Batch{}
			var pending []backfill
			for i, r := range rows {
				row, set, err := prepare(table, r, cols[table])
				if err != nil {
					return fmt.Errorf("seed: %s row %d: %w", table, i+1, err)
				}
				sql, args, err := insertSQL(table, row, cols[table], mode)
				if err != nil {
					return fmt.Errorf("seed: %s row %d: %w", table, i+1, err)
				}
				batch.Queue(sql, args...)
				pending = append(pending, backfill{table: table, id: r.Str("id"), set: set})
			}
			br := tx.SendBatch(ctx, batch)
			for i := range rows {
				tag, err := br.Exec()
				if err != nil {
					_ = br.Close()
					return fmt.Errorf("seed: insert %s row %d (%s): %w", table, i+1, describe(rows[i]), err)
				}
				if tag.RowsAffected() == 1 {
					res.Inserted[table]++
					if k := rowKey(table, rows[i]); k != "" {
						if inserted[table] == nil {
							inserted[table] = map[string]bool{}
						}
						inserted[table][k] = true
					}
					if pending[i].set != nil {
						later = append(later, pending[i]) // upsert back-fills only the rows it inserted
					}
				}
			}
			if err := br.Close(); err != nil {
				return fmt.Errorf("seed: insert %s: %w", table, err)
			}
		}
		for _, b := range later {
			if err := backfillRow(ctx, tx, b.table, b.id, b.set, cols[b.table]); err != nil {
				return err
			}
		}
		if res.Counters, err = deriveCounters(ctx, tx, ids(tables["tasks"])); err != nil {
			return err
		}
		res.TemporaryUserInserted = p.TemporaryUserID != "" && inserted["users"][p.TemporaryUserID]
		var trips, standby map[string]bool // nil: every seeded row (insert mode)
		if mode == ModeUpsert {
			trips, standby = inserted["trip_billing_snapshots"], inserted["standby_records"]
			if trips == nil {
				trips = map[string]bool{}
			}
			if standby == nil {
				standby = map[string]bool{}
			}
		}
		mism, err := CheckEngine(ctx, tx, engineTrips(tables, trips), engineStandby(tables, standby))
		if err != nil {
			return err
		}
		if len(mism) > 0 {
			lines := make([]string, len(mism))
			for i, m := range mism {
				lines[i] = m.String()
			}
			return fmt.Errorf("seed: the billing engine does not reproduce %d seeded value(s), load rolled back:\n  %s",
				len(mism), strings.Join(lines, "\n  "))
		}
		return nil
	})
	res.Duration = time.Since(start)
	return res, err
}

// pricedTables are the tables whose generated rows carry engine prices (the trips' billing party and date, their
// snapshots and multi-drop breakdown, the standby records' inline price): an upsert prices them from the database
// before the first of them is written. pricingInputs are what the engine reads (loadEngineInputs).
var (
	pricedTables  = map[string]bool{"trip_records": true, "trip_billing_snapshots": true, "trip_billing_stop_breakdown": true, "standby_records": true}
	pricingInputs = []string{"billing_parties", "hubs", "hub_name_aliases", "customer_rate_entries",
		"customer_fuel_rate_adjustments", "customer_service_fees", "standby_rate_entries"}
)

// upsertParents are the rows an upsert writes only under a payroll run it inserted itself. The lines of a run are
// writable only while the run is draft (their insert trigger fires before ON CONFLICT is decided); the penalty
// applications (immediate FK), the payout ledger rows (deferred FK transactions_payroll_fk) and the deducted
// advances point at their run. A run the database already holds under the same id (an earlier top-up) keeps its own
// children; one this load did not write (its key held by an application run, heldHRChains) would leave them pointing
// at nothing. A row whose parent column is empty (a transaction or advance of no run) is not a child here.
var upsertParents = map[string][2]string{
	"payroll_line_items":           {"payroll_runs", "payroll_run_id"},
	"payroll_penalty_applications": {"payroll_runs", "payroll_run_id"},
	"transactions":                 {"payroll_runs", "payroll_run_id"},
	"driver_advances":              {"payroll_runs", "payroll_run_id"},
}

// keepWritable drops the rows an upsert must not attempt (upsertParents).
func keepWritable(table string, rows []Row, inserted map[string]map[string]bool) []Row {
	parent, ok := upsertParents[table]
	if !ok {
		return rows
	}
	out := make([]Row, 0, len(rows))
	for _, r := range rows {
		if ref := r.Str(parent[1]); ref == "" || inserted[parent[0]][ref] {
			out = append(out, r)
		}
	}
	return out
}

// heldRuns are the plan's payroll run keys the database holds under another id (HeldRun), in plan order.
func heldRuns(ctx context.Context, tx pgx.Tx, runs []Row) ([]HeldRun, error) {
	var drivers []string
	seen := map[string]bool{}
	for _, r := range runs {
		if d := r.Str("driver_id"); d != "" && !seen[d] {
			seen[d] = true
			drivers = append(drivers, d)
		}
	}
	if len(drivers) == 0 {
		return nil, nil
	}
	rows, err := tx.Query(ctx, `SELECT id::text, driver_id::text, pay_period, pay_round FROM payroll_runs
		WHERE driver_id = ANY($1::uuid[])`, drivers)
	if err != nil {
		return nil, err
	}
	have := map[string]string{} // driver|period|round -> id
	if err := scanAll(rows, func(r pgx.Rows) error {
		var id, driver, period, round string
		if err := r.Scan(&id, &driver, &period, &round); err != nil {
			return err
		}
		have[driver+"|"+period+"|"+round] = id
		return nil
	}); err != nil {
		return nil, err
	}
	var out []HeldRun
	for _, r := range runs {
		k := r.Str("driver_id") + "|" + r.Str("pay_period") + "|" + r.Str("pay_round")
		if id, ok := have[k]; ok && id != r.Str("id") {
			out = append(out, HeldRun{DriverID: r.Str("driver_id"), Period: r.Str("pay_period"), Round: r.Str("pay_round"),
				ExistingID: id, PlanID: r.Str("id")})
		}
	}
	return out, nil
}

// hrChains are the plan's HR rows an upsert skips: every payroll run and penalty of a driver with a held run key, and
// the rows that point at those runs or penalties. A driver's rounds are one chain (an R2 run deducts the installments
// the earlier approvals left, and the penalty balances assume every approval of the plan), so a top-up never writes
// part of it next to a run the application owns; the database keeps that driver's payroll as the application wrote it.
type hrChains struct {
	drivers, runs, penalties map[string]bool
}

// heldHRChains are the chains of the drivers of held (none when held is empty).
func heldHRChains(tables map[string][]Row, held []HeldRun) hrChains {
	c := hrChains{drivers: map[string]bool{}, runs: map[string]bool{}, penalties: map[string]bool{}}
	for _, h := range held {
		c.drivers[h.DriverID] = true
	}
	if len(c.drivers) == 0 {
		return c
	}
	for _, r := range tables["payroll_runs"] {
		if c.drivers[r.Str("driver_id")] {
			c.runs[r.Str("id")] = true
		}
	}
	for _, r := range tables["driver_penalties"] {
		if c.drivers[r.Str("driver_id")] {
			c.penalties[r.Str("id")] = true
		}
	}
	return c
}

// keep drops the rows of table that belong to a skipped chain.
func (c hrChains) keep(table string, rows []Row) []Row {
	if len(c.drivers) == 0 {
		return rows
	}
	var skip func(Row) bool
	switch table {
	case "payroll_runs", "driver_penalties":
		skip = func(r Row) bool { return c.drivers[r.Str("driver_id")] }
	case "payroll_line_items", "transactions", "driver_advances":
		skip = func(r Row) bool { return c.runs[r.Str("payroll_run_id")] }
	case "payroll_penalty_applications":
		skip = func(r Row) bool { return c.runs[r.Str("payroll_run_id")] || c.penalties[r.Str("penalty_id")] }
	default:
		return rows
	}
	out := make([]Row, 0, len(rows))
	for _, r := range rows {
		if !skip(r) {
			out = append(out, r)
		}
	}
	return out
}

// rowKey is the key a load records an inserted row under: its id, or the trip of a billing snapshot (keyed by
// trip_id); "" for the other composite keys, which nothing looks up.
func rowKey(table string, r Row) string {
	if id := r.Str("id"); id != "" {
		return id
	}
	if table == "trip_billing_snapshots" {
		return r.Str("trip_id")
	}
	return ""
}

// CheckNamespace runs sameNamespace in a read-only transaction: cmd/seed calls it before it writes any object
// into a database it does not reset (Load repeats it inside the load transaction).
func CheckNamespace(ctx context.Context, etl db.Beginner, p *Plan) error {
	return db.WithSystem(ctx, etl, nil, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `SET TRANSACTION READ ONLY`); err != nil {
			return err
		}
		return sameNamespace(ctx, tx, p)
	})
}

// sameNamespace refuses a load into a database seeded with another SEED_NAMESPACE (or OWN_FLEET_TENANT_ID):
// the natural keys (tenant code, e-mail, hub source_id, invoice and trip numbers) and the singletons
// (tenants_one_own_fleet, companies_owner_one) are the same in every namespace, so two seeded datasets never
// share a database and an upsert would skip the parents and fail on their children's foreign keys.
func sameNamespace(ctx context.Context, tx pgx.Tx, p *Plan) error {
	rows, err := tx.Query(ctx, `SELECT id FROM tenants WHERE kind = 'own_fleet'`)
	if err != nil {
		return err
	}
	var found []uuid.UUID
	if err := scanAll(rows, func(r pgx.Rows) error {
		var id uuid.UUID
		if err := r.Scan(&id); err != nil {
			return err
		}
		found = append(found, id)
		return nil
	}); err != nil {
		return err
	}
	want := p.Symbols.MustID(ownFleetSymbol)
	for _, id := range found {
		if id != want {
			return fmt.Errorf("seed: this database holds the own-fleet tenant %s, the plan's is %s: it was seeded in another "+
				"SEED_NAMESPACE (or with another OWN_FLEET_TENANT_ID); load with the same namespace, or --reset a local database "+
				"(Appendix D §D.1.3)", id, want)
		}
	}
	return nil
}

// describe names a row in an error without its values (no credential reaches a log line).
func describe(r Row) string {
	for _, k := range []string{"id", "trip_id", "user_id", "key", "alias", "broadcast_id", "payroll_run_id", "config_id"} {
		if v := r.Str(k); v != "" {
			return k + "=" + v
		}
	}
	return "composite key"
}

// backfillRow writes the deferred columns of one row.
func backfillRow(ctx context.Context, tx pgx.Tx, table, id string, set Row, cols map[string]column) error {
	names := set.Columns()
	parts := make([]string, 0, len(names))
	args := make([]any, 0, len(names)+1)
	for _, name := range names {
		arg, ph, err := param(set[name], cols[name], len(args)+1)
		if err != nil {
			return fmt.Errorf("seed: back-fill %s.%s: %w", table, name, err)
		}
		args = append(args, arg)
		parts = append(parts, pgx.Identifier{name}.Sanitize()+" = "+ph)
	}
	args = append(args, id)
	sql := fmt.Sprintf("UPDATE %s SET %s WHERE id = $%d::uuid", pgx.Identifier{table}.Sanitize(), strings.Join(parts, ", "), len(args))
	tag, err := tx.Exec(ctx, sql, args...)
	if err != nil {
		return fmt.Errorf("seed: back-fill %s %s: %w", table, id, err)
	}
	if tag.RowsAffected() != 1 {
		return fmt.Errorf("seed: back-fill %s %s: row not found", table, id)
	}
	return nil
}

// deriveCounters sets task_number_counters to the highest padded sequence per (task type, Bangkok plan
// date) of the seeded tasks, so the next POST /v1/tasks continues the numbering (R10). Legacy unpadded
// numbers do not count. An existing higher counter (a shared database) is kept.
func deriveCounters(ctx context.Context, tx pgx.Tx, taskIDs []uuid.UUID) (int, error) {
	tag, err := tx.Exec(ctx, `INSERT INTO task_number_counters AS c (task_type, plan_date, last_seq)
		SELECT task_type, plan_date, max((regexp_match(task_no, '-([0-9]{3,})$'))[1]::int)
		FROM tasks WHERE id = ANY($1::uuid[]) AND task_no ~ '^(FM|LH)-[0-9]{8}-[0-9]{3,}$'
		GROUP BY task_type, plan_date
		ON CONFLICT (task_type, plan_date) DO UPDATE SET last_seq = GREATEST(c.last_seq, EXCLUDED.last_seq)`, taskIDs)
	if err != nil {
		return 0, fmt.Errorf("seed: task_number_counters: %w", err)
	}
	return int(tag.RowsAffected()), nil
}

// ids are the ids of rows.
func ids(rows []Row) []uuid.UUID {
	out := make([]uuid.UUID, 0, len(rows))
	for _, r := range rows {
		if id, err := uuid.Parse(r.Str("id")); err == nil {
			out = append(out, id)
		}
	}
	return out
}

// engineTrips are the seeded snapshots the engine must reproduce (computed_by other than etl and
// manual_edit, which stand for legacy writers and are inserted verbatim), limited to the trips in only when
// it is not nil (an upsert checks the snapshots it inserted). Never nil: a nil slice selects every row of
// the database in CheckEngine.
func engineTrips(tables map[string][]Row, only map[string]bool) []uuid.UUID {
	out := []uuid.UUID{}
	for _, r := range tables["trip_billing_snapshots"] {
		if by := r.Str("computed_by"); by == "etl" || by == "manual_edit" {
			continue
		}
		if only != nil && !only[r.Str("trip_id")] {
			continue
		}
		if id, err := uuid.Parse(r.Str("trip_id")); err == nil {
			out = append(out, id)
		}
	}
	return out
}

// engineStandby are the seeded completed standby records with a price or a stored reason, limited to the
// records in only when it is not nil. Never nil, like engineTrips.
func engineStandby(tables map[string][]Row, only map[string]bool) []uuid.UUID {
	out := []uuid.UUID{}
	for _, r := range tables["standby_records"] {
		if r.Str("status") != "completed" || (r["billing_estimate_thb"] == nil && r["billing_unpriced_reason"] == nil) {
			continue
		}
		if only != nil && !only[r.Str("id")] {
			continue
		}
		if id, err := uuid.Parse(r.Str("id")); err == nil {
			out = append(out, id)
		}
	}
	return out
}
