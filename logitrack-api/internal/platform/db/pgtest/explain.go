package pgtest

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

// Explainer is a DBTX for sqlc's generated Queries (Exec, Query, QueryRow) that plans each statement instead of running
// it: EXPLAIN (FORMAT JSON) with the call's own arguments, on Tx. A generated method therefore runs its exact SQL with
// real parameter types and values (a custom plan, as the first executions of a cached statement get), writes nothing,
// and sees no rows: Query yields an empty result and QueryRow pgx.ErrNoRows, which callers ignore. The plans pile up in
// Plans, newest last; the index check of a domain (T27: "EXPLAIN of each live query shape uses an index") walks them.
// Tx is usually a db.WithPrincipal transaction, so the plans include the caller's RLS policies, with
// SET LOCAL enable_seqscan = off to make a small fixture plan like a large table would.
type Explainer struct {
	Tx    pgx.Tx
	Plans []Plan
}

// Plan is the planner's answer for one statement.
type Plan struct {
	SQL  string
	Root PlanNode
}

// PlanNode is one node of EXPLAIN (FORMAT JSON); only the fields the checks read.
type PlanNode struct {
	NodeType     string     `json:"Node Type"`
	RelationName string     `json:"Relation Name"`
	Alias        string     `json:"Alias"`
	IndexName    string     `json:"Index Name"`
	IndexCond    string     `json:"Index Cond"`
	Filter       string     `json:"Filter"`
	Parent       string     `json:"Parent Relationship"`
	Plans        []PlanNode `json:"Plans"`
}

// Walk calls fn for n and every node below it, depth first.
func (n PlanNode) Walk(fn func(PlanNode)) {
	fn(n)
	for _, c := range n.Plans {
		c.Walk(fn)
	}
}

// SeqScans are the relations of tables read by a sequential scan anywhere in the plan, sorted, without repeats.
func (p Plan) SeqScans(tables ...string) []string {
	var out []string
	p.Root.Walk(func(n PlanNode) {
		if n.NodeType == "Seq Scan" && (len(tables) == 0 || slices.Contains(tables, n.RelationName)) &&
			!slices.Contains(out, n.RelationName) {
			out = append(out, n.RelationName)
		}
	})
	slices.Sort(out)
	return out
}

// Indexes are the indexes the plan reads (index, index-only and bitmap index scans), sorted, without repeats.
func (p Plan) Indexes() []string {
	var out []string
	p.Root.Walk(func(n PlanNode) {
		if n.IndexName != "" && !slices.Contains(out, n.IndexName) {
			out = append(out, n.IndexName)
		}
	})
	slices.Sort(out)
	return out
}

// String is the plan as an indented tree, for failure messages.
func (p Plan) String() string {
	var b strings.Builder
	var walk func(n PlanNode, depth int)
	walk = func(n PlanNode, depth int) {
		fmt.Fprintf(&b, "%s%s", strings.Repeat("  ", depth), n.NodeType)
		if n.RelationName != "" {
			fmt.Fprintf(&b, " on %s", n.RelationName)
		}
		if n.IndexName != "" {
			fmt.Fprintf(&b, " using %s", n.IndexName)
		}
		if n.IndexCond != "" {
			fmt.Fprintf(&b, " [cond %s]", n.IndexCond)
		}
		if n.Filter != "" {
			fmt.Fprintf(&b, " [filter %s]", n.Filter)
		}
		b.WriteByte('\n')
		for _, c := range n.Plans {
			walk(c, depth+1)
		}
	}
	walk(p.Root, 0)
	return b.String()
}

// Last is the plan of the latest statement (zero Plan when none was explained).
func (e *Explainer) Last() Plan {
	if len(e.Plans) == 0 {
		return Plan{}
	}
	return e.Plans[len(e.Plans)-1]
}

func (e *Explainer) explain(ctx context.Context, sql string, args ...any) error {
	var raw []byte
	if err := e.Tx.QueryRow(ctx, "EXPLAIN (FORMAT JSON) "+sql, args...).Scan(&raw); err != nil {
		return fmt.Errorf("pgtest: explain: %w", err)
	}
	var doc []struct {
		Plan PlanNode `json:"Plan"`
	}
	if err := json.Unmarshal(raw, &doc); err != nil || len(doc) != 1 {
		return fmt.Errorf("pgtest: explain: unexpected plan document %s: %v", raw, err)
	}
	e.Plans = append(e.Plans, Plan{SQL: sql, Root: doc[0].Plan})
	return nil
}

// Exec plans sql and reports an empty command tag.
func (e *Explainer) Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error) {
	if err := e.explain(ctx, sql, args...); err != nil {
		return pgconn.CommandTag{}, err
	}
	return pgconn.NewCommandTag("EXPLAIN"), nil
}

// Query plans sql and yields no rows.
func (e *Explainer) Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error) {
	if err := e.explain(ctx, sql, args...); err != nil {
		return nil, err
	}
	return &noRows{}, nil
}

// QueryRow plans sql; its Scan reports pgx.ErrNoRows (or the planning error).
func (e *Explainer) QueryRow(ctx context.Context, sql string, args ...any) pgx.Row {
	if err := e.explain(ctx, sql, args...); err != nil {
		return errRow{err}
	}
	return errRow{pgx.ErrNoRows}
}

type errRow struct{ err error }

func (r errRow) Scan(...any) error { return r.err }

// noRows is an exhausted result set. It embeds the interface (nil) so a method pgx adds to Rows later still compiles
// (the interface is outside pgx's semantic-version promise); the generated code calls only the methods below.
type noRows struct {
	pgx.Rows
	closed bool
}

func (r *noRows) Close()                        { r.closed = true }
func (r *noRows) Err() error                    { return nil }
func (r *noRows) CommandTag() pgconn.CommandTag { return pgconn.NewCommandTag("EXPLAIN") }
func (r *noRows) Next() bool                    { r.closed = true; return false }
func (r *noRows) Scan(...any) error             { return errors.New("pgtest: explain: no row to scan") }
func (r *noRows) Values() ([]any, error)        { return nil, errors.New("pgtest: explain: no row") }
