package seed

import (
	"bytes"
	"crypto/sha256"
	"fmt"
	"image"
	"image/color"
	"image/draw"
	"image/jpeg"
	"image/png"
	"strconv"

	"golang.org/x/image/font"
	"golang.org/x/image/font/opentype"
	"golang.org/x/image/math/fixed"
)

// Placeholder media (developer-spec.md §14.5, Appendix D §D.1.6): every byte is a function of the object key
// and its overlay text, so two runs with the same Go toolchain write the same objects. Stability across Go
// versions is not promised by the standard encoders, which is why --verify compares the store with
// file_objects and never with hard-coded hashes (except the all-zero APK).

// Sizes of the placeholders.
const (
	PhotoWidth, PhotoHeight = 640, 480
	AssetSize               = 512
	jpegQuality             = 70
	// APKSize is the all-zero placeholder APK (1 MiB).
	APKSize = 1 << 20
	// APKSHA256 is the sha256 of APKSize zero bytes.
	APKSHA256 = "30e14955ebf1352266dc2ff8067e68104607e750abb9d3b36582b8af909fcb58"
)

// Media draws the placeholders with the embedded Sarabun Regular (OFL, cmd/seed/assets).
type Media struct {
	font *opentype.Font
}

// NewMedia parses the TrueType font the overlays are drawn with.
func NewMedia(ttf []byte) (*Media, error) {
	f, err := opentype.Parse(ttf)
	if err != nil {
		return nil, fmt.Errorf("seed: overlay font: %w", err)
	}
	return &Media{font: f}, nil
}

// background is a light colour derived from the object key.
func background(key string) color.RGBA {
	h := sha256.Sum256([]byte(key))
	return color.RGBA{R: 140 + h[0]%100, G: 140 + h[1]%100, B: 140 + h[2]%100, A: 255}
}

// canvas paints the background and the overlay: a dark band with the overlay line and, below it in a
// smaller size, the object key.
func (m *Media) canvas(w, h int, key, overlay string) (*image.RGBA, error) {
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	draw.Draw(img, img.Bounds(), &image.Uniform{C: background(key)}, image.Point{}, draw.Src)
	band := image.Rect(0, h/2-56, w, h/2+56)
	draw.Draw(img, band, &image.Uniform{C: color.RGBA{R: 24, G: 32, B: 48, A: 255}}, image.Point{}, draw.Src)
	if err := m.text(img, overlay, w, h/2+4, 30, color.RGBA{R: 255, G: 255, B: 255, A: 255}); err != nil {
		return nil, err
	}
	if err := m.text(img, key, w, h/2+38, 15, color.RGBA{R: 200, G: 208, B: 220, A: 255}); err != nil {
		return nil, err
	}
	if err := m.text(img, "LogiTrack seed placeholder", w, h-24, 16, color.RGBA{R: 40, G: 40, B: 40, A: 255}); err != nil {
		return nil, err
	}
	return img, nil
}

// text draws s centred on the baseline y, shrinking the size until it fits the width with a margin.
func (m *Media) text(img *image.RGBA, s string, w, y int, size float64, c color.Color) error {
	for ; ; size-- {
		face, err := opentype.NewFace(m.font, &opentype.FaceOptions{Size: size, DPI: 72, Hinting: font.HintingNone})
		if err != nil {
			return fmt.Errorf("seed: overlay face: %w", err)
		}
		width := font.MeasureString(face, s).Ceil()
		if width <= w-32 || size <= 8 {
			d := &font.Drawer{Dst: img, Src: image.NewUniform(c), Face: face, Dot: fixed.P(max(16, (w-width)/2), y)}
			d.DrawString(s)
			return face.Close()
		}
		if err := face.Close(); err != nil {
			return err
		}
	}
}

// JPEG is a 640x480 placeholder photo (quality 70).
func (m *Media) JPEG(key, overlay string) ([]byte, error) {
	img, err := m.canvas(PhotoWidth, PhotoHeight, key, overlay)
	if err != nil {
		return nil, err
	}
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, img, &jpeg.Options{Quality: jpegQuality}); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// PNG is a 512x512 placeholder company asset (logo, stamp, signature).
func (m *Media) PNG(key, overlay string) ([]byte, error) {
	img, err := m.canvas(AssetSize, AssetSize, key, overlay)
	if err != nil {
		return nil, err
	}
	var buf bytes.Buffer
	enc := png.Encoder{CompressionLevel: png.BestSpeed}
	if err := enc.Encode(&buf, img); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// PDF is a one-page A4 placeholder document showing a placeholder JPEG. The statement renderer of
// documents.render arrives with T39; until then the seed writes this page for statement_documents.
func (m *Media) PDF(key, overlay string) ([]byte, error) {
	jpg, err := m.JPEG(key, overlay)
	if err != nil {
		return nil, err
	}
	return pdfWithJPEG(jpg, PhotoWidth, PhotoHeight), nil
}

// pdfWithJPEG writes a minimal PDF 1.4 file: one A4 page whose content draws the JPEG (DCTDecode) across the
// upper part of the page. Offsets in the cross-reference table are exact, so readers need no repair.
func pdfWithJPEG(jpg []byte, w, h int) []byte {
	var buf bytes.Buffer
	var offsets []int
	obj := func(body func()) {
		offsets = append(offsets, buf.Len())
		fmt.Fprintf(&buf, "%d 0 obj\n", len(offsets))
		body()
		buf.WriteString("\nendobj\n")
	}
	buf.WriteString("%PDF-1.4\n%\xe2\xe3\xcf\xd3\n")
	obj(func() { buf.WriteString("<< /Type /Catalog /Pages 2 0 R >>") })
	obj(func() { buf.WriteString("<< /Type /Pages /Kids [3 0 R] /Count 1 >>") })
	obj(func() {
		buf.WriteString("<< /Type /Page /Parent 2 0 R /MediaBox [0 0 595 842] /Resources << /XObject << /Im0 4 0 R >> >> /Contents 5 0 R >>")
	})
	obj(func() {
		fmt.Fprintf(&buf, "<< /Type /XObject /Subtype /Image /Width %d /Height %d /ColorSpace /DeviceRGB /BitsPerComponent 8 /Filter /DCTDecode /Length %d >>\nstream\n", w, h, len(jpg))
		buf.Write(jpg)
		buf.WriteString("\nendstream")
	})
	content := "q 535 0 0 401 30 411 cm /Im0 Do Q"
	obj(func() {
		fmt.Fprintf(&buf, "<< /Length %d >>\nstream\n%s\nendstream", len(content), content)
	})
	xref := buf.Len()
	fmt.Fprintf(&buf, "xref\n0 %d\n0000000000 65535 f \n", len(offsets)+1)
	for _, o := range offsets {
		fmt.Fprintf(&buf, "%010d 00000 n \n", o)
	}
	fmt.Fprintf(&buf, "trailer\n<< /Size %d /Root 1 0 R >>\nstartxref\n%s\n%%%%EOF\n", len(offsets)+1, strconv.Itoa(xref))
	return buf.Bytes()
}

// APK is the all-zero placeholder APK of mobile_app_releases (Appendix D §D.1.6).
func APK() []byte { return make([]byte, APKSize) }
