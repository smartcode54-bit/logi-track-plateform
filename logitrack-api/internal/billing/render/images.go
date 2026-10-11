package render

import (
	"bytes"
	"errors"
	"image"
	"image/color"
	stddraw "image/draw"
	"image/jpeg"
	"image/png"

	"golang.org/x/image/draw"
)

// Photo limits of the Shopee report (loadAndDownscale, web:lib/shopeeExpressReport.ts:50-83): the longest side at most
// 1600 px, re-encoded as JPEG quality 75 on a white background.
const (
	PhotoMaxDim      = 1600
	PhotoJPEGQuality = 75
)

// MaxPixels bounds the images a renderer decodes (a 50-megapixel photo is far above any phone camera): a larger one is
// refused before it is decoded, so a crafted header cannot make the worker allocate gigabytes.
const MaxPixels = 50_000_000

// ErrNotImage is a file that is not a JPEG or PNG image, or one above MaxPixels. The driver app uploads JPEG (it
// compresses every photo, main spec §9); WebP is not decoded (its decoder is the subject of GO-2026-6222 in the
// golang.org/x/image this module pins), so a WebP photo is left out of a report like any unreadable one.
var ErrNotImage = errors.New("render: not a JPEG or PNG image within the size limit")

// checkPixels refuses an image whose header declares more than MaxPixels.
func checkPixels(data []byte) (image.Config, string, error) {
	cfg, format, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil || cfg.Width <= 0 || cfg.Height <= 0 || int64(cfg.Width)*int64(cfg.Height) > MaxPixels {
		return image.Config{}, "", ErrNotImage
	}
	return cfg, format, nil
}

// Downscale decodes a photo, scales it so its longest side is at most maxDim pixels (never up), paints it on white and
// encodes it as a JPEG of quality q: what the browser did on a canvas before embedding a run-sheet photo.
func Downscale(data []byte, maxDim, q int) (Image, error) {
	if _, _, err := checkPixels(data); err != nil {
		return Image{}, err
	}
	src, _, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		return Image{}, ErrNotImage
	}
	b := src.Bounds()
	w, h := b.Dx(), b.Dy()
	if w <= 0 || h <= 0 {
		return Image{}, ErrNotImage
	}
	scale := min(1, float64(maxDim)/float64(max(w, h)))
	dw, dh := max(1, int(float64(w)*scale+0.5)), max(1, int(float64(h)*scale+0.5))
	dst := image.NewRGBA(image.Rect(0, 0, dw, dh))
	stddraw.Draw(dst, dst.Bounds(), image.NewUniform(color.White), image.Point{}, stddraw.Src)
	draw.ApproxBiLinear.Scale(dst, dst.Bounds(), src, b, draw.Over, nil)
	var out bytes.Buffer
	if err := jpeg.Encode(&out, dst, &jpeg.Options{Quality: q}); err != nil {
		return Image{}, err
	}
	return Image{Data: out.Bytes(), W: dw, H: dh}, nil
}

// Embeddable prepares a company stamp or signature: a JPEG or a PNG is kept as it is (a PNG keeps its transparency
// over the signature lines), anything else readable is converted to PNG. Its pixel size is read from the header.
func Embeddable(data []byte) (Image, error) {
	cfg, format, err := checkPixels(data)
	if err != nil {
		return Image{}, err
	}
	if format == "jpeg" || format == "png" {
		return Image{Data: data, W: cfg.Width, H: cfg.Height}, nil
	}
	src, _, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		return Image{}, ErrNotImage
	}
	var out bytes.Buffer
	if err := png.Encode(&out, src); err != nil {
		return Image{}, err
	}
	return Image{Data: out.Bytes(), W: cfg.Width, H: cfg.Height}, nil
}
