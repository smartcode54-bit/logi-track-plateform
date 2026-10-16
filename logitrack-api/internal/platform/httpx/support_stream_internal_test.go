package httpx

import (
	"context"
	"errors"
	"io"
	"strings"
	"testing"
)

func TestScopedReaderChecksEveryBoundedChunk(t *testing.T) {
	calls := 0
	revoked := errors.New("revoked")
	reader := &scopedReader{ReadCloser: io.NopCloser(strings.NewReader(strings.Repeat("x", 64<<10))), ctx: context.Background(), check: func(context.Context) error {
		calls++
		if calls == 2 {
			return revoked
		}
		return nil
	}}
	buffer := make([]byte, 128<<10)
	n, err := reader.Read(buffer)
	if n != 32<<10 || err != nil {
		t.Fatal(n, err)
	}
	n, err = reader.Read(buffer)
	if n != 0 || !errors.Is(err, revoked) {
		t.Fatal("revocation emitted another chunk", n, err)
	}
}
