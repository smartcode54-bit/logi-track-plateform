package storage

import (
	"bytes"
	"image"
	"image/color"
	"image/gif"
	"image/jpeg"
	"image/png"
	"testing"
)

func TestSupportFileBytesRejectsSpoofsAndStripsImageMetadata(t *testing.T) {
	img := image.NewRGBA(image.Rect(0, 0, 2, 2))
	img.Set(0, 0, color.RGBA{R: 255, A: 255})
	for _, ct := range []string{"image/png", "image/jpeg", "image/gif"} {
		t.Run(ct, func(t *testing.T) {
			var b bytes.Buffer
			switch ct {
			case "image/png":
				_ = png.Encode(&b, img)
			case "image/jpeg":
				_ = jpeg.Encode(&b, img, nil)
			case "image/gif":
				_ = gif.Encode(&b, img, nil)
			}
			raw := append(b.Bytes(), []byte("PRIVATE_IMAGE_METADATA_SENTINEL")...)
			clean, err := supportFileBytes(raw, ct)
			if err != nil {
				t.Fatal(err)
			}
			if bytes.Contains(clean, []byte("PRIVATE_IMAGE_METADATA_SENTINEL")) {
				t.Fatal("metadata retained")
			}
			if _, _, err := image.Decode(bytes.NewReader(clean)); err != nil {
				t.Fatal(err)
			}
			if _, err := supportFileBytes([]byte("<html>private</html>"), ct); err == nil {
				t.Fatal("spoofed type accepted")
			}
		})
	}
	if _, err := supportFileBytes([]byte("<html>not PDF</html>"), "application/pdf"); err == nil {
		t.Fatal("HTML accepted as PDF")
	}
	if _, err := supportFileBytes([]byte("%PDF-1.7\ntruncated"), "application/pdf"); err == nil {
		t.Fatal("truncated PDF accepted")
	}
	body := []byte("%PDF-1.7\nfixture\n%%EOF")
	if out, err := supportFileBytes(body, "application/pdf"); err != nil || !bytes.Equal(out, body) {
		t.Fatal(err)
	}
}
