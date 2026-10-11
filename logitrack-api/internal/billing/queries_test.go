package billing

import (
	"os"
	"strings"
	"testing"
)

// The statement inserts its lines under the caller's RLS (InsertStatementLines) and refuses when the same set, counted
// without RLS (StatementInvoiceSet), holds more rows. The two must select the same rows, so their FROM ( ... ) x bodies
// are the same text.
func TestInvoiceSetQueriesShareTheirPredicate(t *testing.T) {
	b, err := os.ReadFile("queries/statements.sql")
	if err != nil {
		t.Fatal(err)
	}
	body := func(name string) string {
		t.Helper()
		src := string(b)
		i := strings.Index(src, "-- name: "+name+" ")
		if i < 0 {
			t.Fatalf("no query %s", name)
		}
		src = src[i:]
		from := strings.Index(src, "\nFROM (\n")
		end := strings.Index(src, "\n) x;")
		if from < 0 || end < from {
			t.Fatalf("%s: no FROM ( ... ) x body", name)
		}
		return src[from:end]
	}
	if a, c := body("InsertStatementLines"), body("StatementInvoiceSet"); a != c {
		t.Fatalf("the invoice-set predicates differ:\n--- InsertStatementLines\n%s\n--- StatementInvoiceSet\n%s", a, c)
	}
}
