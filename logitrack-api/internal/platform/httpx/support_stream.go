package httpx

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"sync"

	"github.com/gofiber/fiber/v3"
)

// StreamSource is resolved/authorized by a domain broker before opening storage.
// Check revalidates the live session, source tenant and consent before each chunk.
type StreamSource struct {
	Tenant      string
	ContentType string
	Size        int64
	KnownSize   bool
	Check       func(context.Context) error
	Open        func(context.Context) (io.ReadCloser, error)
}
type StreamAdapter struct {
	id, domain   string
	contentTypes []string
	masked       bool
	resolve      func(fiber.Ctx) (StreamSource, error)
}

var streamAdapters sync.Map

func NewStreamAdapter(id, domain string, contentTypes []string, masked bool, resolve func(fiber.Ctx) (StreamSource, error)) *StreamAdapter {
	if id == "" || !slices.Contains([]string{"dashboard", "fleet", "drivers", "maintenance", "partners"}, domain) || len(contentTypes) == 0 || resolve == nil {
		panic("httpx: invalid stream adapter")
	}
	adapter := &StreamAdapter{id: id, domain: domain, contentTypes: slices.Clone(contentTypes), masked: masked, resolve: resolve}
	if _, loaded := streamAdapters.LoadOrStore(id, adapter); loaded {
		panic("httpx: duplicate stream adapter")
	}
	return adapter
}

type scopedReader struct {
	io.ReadCloser
	ctx   context.Context
	check func(context.Context) error
}

func (r *scopedReader) Read(p []byte) (int, error) {
	if err := r.check(r.ctx); err != nil {
		return 0, err
	}
	return r.ReadCloser.Read(p[:min(len(p), 32<<10)])
}

type streamReceipt struct {
	reader  *scopedReader
	digest  [32]byte
	adapter *StreamAdapter
}
type streamReceiptKey struct{}

// Stream is the sole admitted streaming writer. The request-local receipt binds
// the registered broker, final headers and exact scoped reader, not a raw-writer flag.
func Stream(c fiber.Ctx, adapter *StreamAdapter) error {
	if adapter == nil {
		return ErrSupportProjectionUnavailable()
	}
	registered, ok := streamAdapters.Load(adapter.id)
	p := EndpointPolicyFrom(c)
	a := ProjectionAudienceFrom(c)
	if !ok || registered != adapter || p.SchemaID != "stream:"+adapter.id || !projectionRequired(c) {
		return ErrSupportProjectionUnavailable()
	}
	source, err := adapter.resolve(c)
	if err != nil {
		return err
	}
	if source.Tenant == "" || source.Check == nil || source.Open == nil || !slices.Contains(adapter.contentTypes, source.ContentType) {
		return ErrSupportViewUnavailable()
	}
	if a.Support && p.Plane != "platform" && source.Tenant != a.Tenant {
		return ErrSupportViewUnavailable()
	}
	if !adapter.masked && (!a.Consents[source.Tenant].Enabled || !a.Support && !a.Platform) {
		return NewError(403, CodeSupportAccessMasked, "support access is masked")
	}
	if err := source.Check(c.Context()); err != nil {
		return err
	}
	reader, err := source.Open(c.Context())
	if err != nil {
		return err
	}
	scoped := &scopedReader{ReadCloser: reader, ctx: c.Context(), check: source.Check}
	cleanProjectionHeaders(c)
	c.Set(fiber.HeaderContentType, source.ContentType)
	c.Set("Content-Security-Policy", "sandbox; default-src 'none'")
	if source.ContentType == "application/pdf" {
		c.Set("Content-Disposition", "attachment; filename=\"document.pdf\"")
	}
	size := -1
	if source.KnownSize {
		start, length, partial, err := streamRange(c.Get(fiber.HeaderRange), source.Size)
		if err != nil {
			_ = reader.Close()
			c.Set("Content-Range", fmt.Sprintf("bytes */%d", source.Size))
			return NewError(http.StatusRequestedRangeNotSatisfiable, "range_not_satisfiable", "range not satisfiable")
		}
		size = int(length)
		if start > 0 {
			if _, err := io.CopyN(io.Discard, scoped, start); err != nil {
				_ = reader.Close()
				return err
			}
		}
		scoped.ReadCloser = &limitedStream{Reader: io.LimitReader(reader, length), Closer: reader}
		c.Set("Accept-Ranges", "bytes")
		if partial {
			c.Status(http.StatusPartialContent)
			c.Set("Content-Range", fmt.Sprintf("bytes %d-%d/%d", start, start+length-1, source.Size))
		}
	}
	if c.Method() == http.MethodHead {
		_ = reader.Close()
		scoped.ReadCloser = io.NopCloser(bytes.NewReader(nil))
	}
	if err := c.SendStream(scoped, size); err != nil {
		_ = reader.Close()
		return err
	}
	c.Locals(streamReceiptKey{}, streamReceipt{reader: scoped, adapter: adapter, digest: responseHeadDigest(c)})
	return nil
}

func admittedStream(c fiber.Ctx) bool {
	proof, ok := c.Locals(streamReceiptKey{}).(streamReceipt)
	if !ok || c.Response().BodyStream() != proof.reader {
		return false
	}
	adapter, ok := streamAdapters.Load(proof.adapter.id)
	return ok && adapter == proof.adapter && EndpointPolicyFrom(c).SchemaID == fmt.Sprint("stream:", proof.adapter.id) && proof.digest == responseHeadDigest(c)
}

// OrdinaryStreamWriter keeps the old transport contract outside projection. The
// guard remains mandatory: a support branch cannot use this writer as an escape.
func OrdinaryStreamWriter(c fiber.Ctx, run func(*bufio.Writer)) error {
	if projectionRequired(c) {
		return ErrSupportProjectionUnavailable()
	}
	return c.SendStreamWriter(run)
}

type limitedStream struct {
	io.Reader
	io.Closer
}

func streamRange(value string, size int64) (start, length int64, partial bool, err error) {
	if value == "" {
		return 0, size, false, nil
	}
	bad := func() (int64, int64, bool, error) { return 0, 0, false, fmt.Errorf("invalid range") }
	v, ok := strings.CutPrefix(value, "bytes=")
	if !ok || strings.ContainsAny(v, ", \t") || size <= 0 {
		return bad()
	}
	a, b, ok := strings.Cut(v, "-")
	if !ok {
		return bad()
	}
	if a == "" {
		n, e := strconv.ParseInt(b, 10, 64)
		if e != nil || n <= 0 {
			return bad()
		}
		n = min(n, size)
		return size - n, n, true, nil
	}
	first, e := strconv.ParseInt(a, 10, 64)
	if e != nil || first < 0 || first >= size {
		return bad()
	}
	last := size - 1
	if b != "" {
		last, e = strconv.ParseInt(b, 10, 64)
		if e != nil || last < first {
			return bad()
		}
		last = min(last, size-1)
	}
	return first, last - first + 1, true, nil
}

// StreamPipe builds a source reader, not a response writer. Only Stream may
// admit that reader into HTTP, checking the registered adapter and each chunk.
func StreamPipe(run func(*bufio.Writer)) io.ReadCloser {
	reader, writer := io.Pipe()
	go func() { defer func() { _ = writer.Close() }(); run(bufio.NewWriterSize(writer, 32<<10)) }()
	return reader
}

// OrdinaryStatus preserves existing bodyless transports outside projection.
func OrdinaryStatus(c fiber.Ctx, status int) error {
	if projectionRequired(c) {
		return ErrSupportProjectionUnavailable()
	}
	return c.SendStatus(status)
}
