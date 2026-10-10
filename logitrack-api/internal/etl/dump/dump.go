package dump

import (
	"bufio"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"time"
)

// Format is the manifest format tag.
const Format = "logitrack-etl-dump/1"

// ManifestName is the manifest file of a dump directory.
const ManifestName = "manifest.json"

// Doc is one Firestore document of a dump.
type Doc struct {
	ID         string
	Path       string // full path below the database root, parents included: drivers/d1/mobile_installations/i1
	CreateTime time.Time
	UpdateTime time.Time
	Fields     map[string]any
}

// Collection is the collection (group) id: the second-to-last path segment.
func (d Doc) Collection() string {
	parts := strings.Split(d.Path, "/")
	if len(parts) < 2 {
		return ""
	}
	return parts[len(parts)-2]
}

// ParentPath is the path of the parent document ("" for a top-level collection).
func (d Doc) ParentPath() string {
	parts := strings.Split(d.Path, "/")
	if len(parts) < 4 {
		return ""
	}
	return strings.Join(parts[:len(parts)-2], "/")
}

type line struct {
	ID         string          `json:"_id"`
	Path       string          `json:"_path"`
	CreateTime *time.Time      `json:"_createTime,omitempty"`
	UpdateTime time.Time       `json:"_updateTime"`
	Fields     json.RawMessage `json:"fields"`
}

// MarshalDoc encodes one NDJSON line (without the newline).
func MarshalDoc(d Doc) ([]byte, error) {
	if err := validDoc(d); err != nil {
		return nil, err
	}
	fields, err := MarshalFields(d.Fields)
	if err != nil {
		return nil, fmt.Errorf("dump: %s: %w", d.Path, err)
	}
	l := line{ID: d.ID, Path: d.Path, UpdateTime: d.UpdateTime.UTC(), Fields: fields}
	if !d.CreateTime.IsZero() {
		ct := d.CreateTime.UTC()
		l.CreateTime = &ct
	}
	return json.Marshal(l)
}

// UnmarshalDoc decodes one NDJSON line.
func UnmarshalDoc(b []byte) (Doc, error) {
	var l line
	if err := json.Unmarshal(b, &l); err != nil {
		return Doc{}, fmt.Errorf("dump: line: %w", err)
	}
	fields, err := UnmarshalFields(l.Fields)
	if err != nil {
		return Doc{}, fmt.Errorf("dump: %s: %w", l.Path, err)
	}
	d := Doc{ID: l.ID, Path: l.Path, UpdateTime: l.UpdateTime.UTC(), Fields: fields}
	if l.CreateTime != nil {
		d.CreateTime = l.CreateTime.UTC()
	}
	if err := validDoc(d); err != nil {
		return Doc{}, err
	}
	return d, nil
}

func validDoc(d Doc) error {
	parts := strings.Split(d.Path, "/")
	if d.ID == "" || len(parts)%2 != 0 || parts[len(parts)-1] != d.ID || slices.Contains(parts, "") {
		return fmt.Errorf("dump: document %q: _path must be collection/id[/collection/id...] ending in _id", d.Path)
	}
	if d.UpdateTime.IsZero() {
		return fmt.Errorf("dump: document %q: _updateTime is required", d.Path)
	}
	return nil
}

// Manifest describes one dump.
type Manifest struct {
	Format      string           `json:"format"`
	ExportedAt  time.Time        `json:"exportedAt"`
	ProjectID   string           `json:"projectId,omitempty"`
	DatabaseID  string           `json:"databaseId,omitempty"`
	Collections []CollectionFile `json:"collections"`
}

// CollectionFile is one collection (or collection group) of a dump.
type CollectionFile struct {
	Name  string `json:"name"`
	Group bool   `json:"group,omitempty"` // collection-group query (subcollections such as mobile_installations)
	File  string `json:"file"`
	Count int    `json:"count"`
	// SHA256 is the digest of File's bytes; `etl dump` always writes it, hand-written fixtures may omit it.
	SHA256 string `json:"sha256,omitempty"`
}

var collectionName = regexp.MustCompile(`^[A-Za-z0-9_-]{1,128}$`)

// Dump is an opened dump directory (a local directory, an embedded fixture, or a downloaded S3 dump).
type Dump struct {
	fsys     fs.FS
	Manifest Manifest
}

// Open reads and checks the manifest of fsys.
func Open(fsys fs.FS) (*Dump, error) {
	b, err := fs.ReadFile(fsys, ManifestName)
	if err != nil {
		return nil, fmt.Errorf("dump: read %s: %w", ManifestName, err)
	}
	var m Manifest
	if err := json.Unmarshal(b, &m); err != nil {
		return nil, fmt.Errorf("dump: %s: %w", ManifestName, err)
	}
	if m.Format != Format {
		return nil, fmt.Errorf("dump: %s: format must be %q", ManifestName, Format)
	}
	if m.ExportedAt.IsZero() {
		return nil, fmt.Errorf("dump: %s: exportedAt is required", ManifestName)
	}
	seen := map[string]bool{}
	for _, c := range m.Collections {
		if !collectionName.MatchString(c.Name) || seen[c.Name] {
			return nil, fmt.Errorf("dump: %s: collection %q is malformed or repeated", ManifestName, c.Name)
		}
		if c.File != filepath.Base(c.File) || (!strings.HasSuffix(c.File, ".ndjson") && !strings.HasSuffix(c.File, ".ndjson.gz")) {
			return nil, fmt.Errorf("dump: %s: collection %q: file must be a .ndjson or .ndjson.gz name in the dump directory", ManifestName, c.Name)
		}
		seen[c.Name] = true
	}
	return &Dump{fsys: fsys, Manifest: m}, nil
}

// OpenDir opens a dump directory on disk.
func OpenDir(dir string) (*Dump, error) { return Open(os.DirFS(dir)) }

// Collection returns the manifest entry of name.
func (d *Dump) Collection(name string) (CollectionFile, bool) {
	for _, c := range d.Manifest.Collections {
		if c.Name == name {
			return c, true
		}
	}
	return CollectionFile{}, false
}

// Names lists the collections of the dump in manifest order.
func (d *Dump) Names() []string {
	out := make([]string, len(d.Manifest.Collections))
	for i, c := range d.Manifest.Collections {
		out[i] = c.Name
	}
	return out
}

// Read returns every document of a collection sorted by path, after checking the manifest's count and
// digest.
func (d *Dump) Read(name string) ([]Doc, error) {
	c, ok := d.Collection(name)
	if !ok {
		return nil, fmt.Errorf("dump: no collection %q", name)
	}
	raw, err := fs.ReadFile(d.fsys, c.File)
	if err != nil {
		return nil, fmt.Errorf("dump: %s: %w", c.File, err)
	}
	if c.SHA256 != "" {
		sum := sha256.Sum256(raw)
		if hex.EncodeToString(sum[:]) != c.SHA256 {
			return nil, fmt.Errorf("dump: %s: sha256 differs from the manifest", c.File)
		}
	}
	var r io.Reader = bytes.NewReader(raw)
	if strings.HasSuffix(c.File, ".gz") {
		zr, err := gzip.NewReader(r)
		if err != nil {
			return nil, fmt.Errorf("dump: %s: %w", c.File, err)
		}
		defer func() { _ = zr.Close() }()
		r = zr
	}
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 64<<10), 64<<20)
	var docs []Doc
	paths := map[string]bool{}
	for n := 1; sc.Scan(); n++ {
		text := bytes.TrimSpace(sc.Bytes())
		if len(text) == 0 {
			continue
		}
		doc, err := UnmarshalDoc(text)
		if err != nil {
			return nil, fmt.Errorf("dump: %s line %d: %w", c.File, n, err)
		}
		if doc.Collection() != name {
			return nil, fmt.Errorf("dump: %s line %d: %s is not in collection %s", c.File, n, doc.Path, name)
		}
		if c.Group != (doc.ParentPath() != "") {
			return nil, fmt.Errorf("dump: %s line %d: %s does not match group=%v", c.File, n, doc.Path, c.Group)
		}
		if paths[doc.Path] {
			return nil, fmt.Errorf("dump: %s line %d: %s is repeated", c.File, n, doc.Path)
		}
		paths[doc.Path] = true
		docs = append(docs, doc)
	}
	if err := sc.Err(); err != nil {
		return nil, fmt.Errorf("dump: %s: %w", c.File, err)
	}
	if len(docs) != c.Count {
		return nil, fmt.Errorf("dump: %s: %d documents, the manifest says %d", c.File, len(docs), c.Count)
	}
	slices.SortFunc(docs, func(a, b Doc) int { return strings.Compare(a.Path, b.Path) })
	return docs, nil
}

// Writer writes a dump directory: gzip NDJSON per collection, then the manifest on Close.
type Writer struct {
	dir string
	m   Manifest
}

// NewWriter creates dir (it must not hold a manifest yet).
func NewWriter(dir string, exportedAt time.Time, projectID, databaseID string) (*Writer, error) {
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return nil, fmt.Errorf("dump: %w", err)
	}
	if _, err := os.Stat(filepath.Join(dir, ManifestName)); err == nil {
		return nil, fmt.Errorf("dump: %s already holds a dump", dir)
	}
	return &Writer{dir: dir, m: Manifest{Format: Format, ExportedAt: exportedAt.UTC(), ProjectID: projectID, DatabaseID: databaseID}}, nil
}

// WriteCollection writes the documents of one collection (sorted by path) and records it.
func (w *Writer) WriteCollection(name string, group bool, docs []Doc) error {
	if !collectionName.MatchString(name) {
		return fmt.Errorf("dump: collection name %q is malformed", name)
	}
	slices.SortFunc(docs, func(a, b Doc) int { return strings.Compare(a.Path, b.Path) })
	file := name + ".ndjson.gz"
	var buf bytes.Buffer
	zw := gzip.NewWriter(&buf)
	zw.ModTime = time.Time{} // equal input, equal bytes
	for _, d := range docs {
		b, err := MarshalDoc(d)
		if err != nil {
			return err
		}
		if _, err := zw.Write(append(b, '\n')); err != nil {
			return err
		}
	}
	if err := zw.Close(); err != nil {
		return err
	}
	sum := sha256.Sum256(buf.Bytes())
	if err := os.WriteFile(filepath.Join(w.dir, file), buf.Bytes(), 0o640); err != nil {
		return fmt.Errorf("dump: %w", err)
	}
	w.m.Collections = append(w.m.Collections, CollectionFile{Name: name, Group: group, File: file, Count: len(docs), SHA256: hex.EncodeToString(sum[:])})
	return nil
}

// Close writes the manifest; the dump is complete only once it exists.
func (w *Writer) Close() error {
	if w.m.Collections == nil {
		return errors.New("dump: no collection was written")
	}
	b, err := json.MarshalIndent(w.m, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(w.dir, ManifestName), append(b, '\n'), 0o640)
}

// Files lists the files of the dump for upload: every collection file, then the manifest.
func (w *Writer) Files() []string {
	out := make([]string, 0, len(w.m.Collections)+1)
	for _, c := range w.m.Collections {
		out = append(out, c.File)
	}
	return append(out, ManifestName)
}

// Dir is the directory being written.
func (w *Writer) Dir() string { return w.dir }
