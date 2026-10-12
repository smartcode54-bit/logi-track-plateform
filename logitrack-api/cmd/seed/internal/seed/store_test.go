package seed

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/storage"
)

// failingStat answers Stat of one key the way an unreachable store does.
type failingStat struct {
	*storage.Local
	key string
}

func (f failingStat) Stat(ctx context.Context, o storage.Object) (storage.Info, error) {
	if o.Key == f.key {
		return storage.Info{}, errors.New("connection refused")
	}
	return f.Local.Stat(ctx, o)
}

// Invariant 9 checks the objects on parallel workers (T35: the demo's 3,676 objects one after another took most of a
// --verify): every kind of problem is still reported, in the order of the rows, among more good objects than workers;
// a store that cannot answer stops the check with its error instead of a report.
func TestCheckObjectRows(t *testing.T) {
	ctx := context.Background()
	local, err := storage.NewLocal(storage.LocalConfig{Dir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = local.Close() }()
	put := func(key string, body []byte) (*int64, *string) {
		t.Helper()
		if err := local.Put(ctx, storage.Object{Bucket: "logitrack", Key: key}, bytes.NewReader(body), int64(len(body)), "image/jpeg"); err != nil {
			t.Fatal(err)
		}
		size, sum := int64(len(body)), sha256.Sum256(body)
		digest := hex.EncodeToString(sum[:])
		return &size, &digest
	}
	row := func(key, status, backend string, size *int64, sha *string) objectRow {
		return objectRow{bucket: "logitrack", key: key, status: status, backend: backend, size: size, sha: sha}
	}
	var rows []objectRow
	for i := range 3 * checkParallelism {
		key := fmt.Sprintf("trips/t%02d/seal.jpg", i)
		size, sha := put(key, []byte("good "+key))
		status := "committed"
		if i%5 == 0 {
			status = "pending"
		}
		rows = append(rows, row(key, status, "local", size, sha))
	}
	wrongSize := int64(1)
	otherSum := strings.Repeat("0", 64)
	_, sizedSha := put("trips/u01/size.jpg", []byte("recorded with another size"))
	shaSize, _ := put("trips/u02/sha.jpg", []byte("recorded with another sha256"))
	legacySize, legacySha := put("trips/u04/legacy.jpg", []byte("an object the ETL never copied"))
	lastSize, lastSha := put("trips/u07/last.jpg", []byte("good, after the problems"))
	rows = append(rows,
		row("trips/u01/size.jpg", "committed", "local", &wrongSize, sizedSha),
		row("trips/u02/sha.jpg", "committed", "local", shaSize, &otherSum),
		row("trips/u03/gone.jpg", "pending", "local", &wrongSize, &otherSum),
		row("trips/u04/legacy.jpg", "missing_at_source", "local", legacySize, legacySha),
		row("trips/u05/legacy.jpg", "missing_at_source", "local", nil, nil),
		row("trips/u06/elsewhere.jpg", "committed", "s3", &wrongSize, &otherSum),
		row("trips/u07/last.jpg", "committed", "local", lastSize, lastSha))

	got, err := checkObjectRows(ctx, rows, Backends{storage.BackendLocal: local})
	if err != nil {
		t.Fatal(err)
	}
	want := []string{
		"trips/u01/size.jpg: size 26, recorded 1",
		"trips/u02/sha.jpg: sha256 " + sumOf("recorded with another sha256") + ", recorded " + otherSum,
		"trips/u03/gone.jpg: pending but no object",
		"trips/u04/legacy.jpg: missing_at_source but the object exists",
		`trips/u06/elsewhere.jpg: seed: storage backend "s3" is not configured`,
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("problems\n have %q\n want %q", got, want)
	}
	if got, err := checkObjectRows(ctx, rows[:3*checkParallelism], Backends{storage.BackendLocal: local}); err != nil || got != nil {
		t.Fatalf("good objects only: %q, %v", got, err)
	}

	down := Backends{storage.BackendLocal: failingStat{Local: local, key: "trips/t07/seal.jpg"}}
	if got, err := checkObjectRows(ctx, rows, down); err == nil || !strings.Contains(err.Error(), "stat trips/t07/seal.jpg: connection refused") || got != nil {
		t.Fatalf("a store that cannot answer: %q, %v; want the stat error and no report", got, err)
	}
}

func sumOf(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])
}
