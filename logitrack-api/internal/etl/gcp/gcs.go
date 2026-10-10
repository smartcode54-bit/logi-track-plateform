package gcp

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"

	"golang.org/x/oauth2"
)

// GCS downloads objects of Cloud Storage through the JSON API (the source of `etl media-copy`, ETL_GCS_BUCKET).
type GCS struct {
	client *http.Client
	tokens oauth2.TokenSource
}

// NewGCS builds the client without I/O.
func NewGCS(client *http.Client, tokens oauth2.TokenSource) (*GCS, error) {
	if tokens == nil {
		return nil, errors.New("gcp: a token source is required")
	}
	if client == nil {
		client = &http.Client{} // downloads are bounded by the caller's context, not a fixed timeout
	}
	return &GCS{client: client, tokens: tokens}, nil
}

// Object is an open download.
type Object struct {
	Body        io.ReadCloser
	Size        int64 // -1 when the server sent no length
	ContentType string
}

// Open starts the download of bucket/name; ErrNotFound when the object does not exist. The caller closes Body.
func (g *GCS) Open(ctx context.Context, bucket, name string) (*Object, error) {
	tok, err := g.tokens.Token()
	if err != nil {
		return nil, errors.New("gcp: storage download: access token unavailable")
	}
	u := StorageURL + "/b/" + url.PathEscape(bucket) + "/o/" + url.PathEscape(name) + "?alt=media"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, fmt.Errorf("gcp: storage download: %w", err)
	}
	tok.SetAuthHeader(req)
	res, err := g.client.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, errors.New("gcp: storage download: transport failure")
	}
	if res.StatusCode != http.StatusOK {
		raw, _ := io.ReadAll(io.LimitReader(res.Body, 64<<10))
		_ = res.Body.Close()
		if res.StatusCode == http.StatusNotFound {
			return nil, fmt.Errorf("%w: storage object", ErrNotFound)
		}
		return nil, &APIError{Op: "storage download", Status: res.StatusCode, Code: statusCode(raw)}
	}
	size := int64(-1)
	if n, err := strconv.ParseInt(res.Header.Get("Content-Length"), 10, 64); err == nil {
		size = n
	}
	return &Object{Body: res.Body, Size: size, ContentType: res.Header.Get("Content-Type")}, nil
}
