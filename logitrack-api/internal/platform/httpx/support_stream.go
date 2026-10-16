package httpx

import (
	"context"
	"fmt"
	"io"
	"slices"
	"sync"

	"github.com/gofiber/fiber/v3"
)

// StreamSource is resolved/authorized by a domain broker before opening storage.
// Check revalidates the live session, source tenant and consent before each chunk.
type StreamSource struct {
	Tenant      string
	ContentType string
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
	if err := c.SendStream(scoped); err != nil {
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
