// Package render draws the billing documents server-side (main spec §9.9, R69, T39): the invoice summary and receipt
// PDFs, the detail XLSX, the ZIP bundle and the Shopee Express report PDF, replacing jsPDF, jspdf-autotable,
// xlsx-js-style and jszip in web:lib/billingDocumentRender.ts and web:lib/shopeeExpressReport.ts. It is the impure half
// of internal/billing/documents: the pure package computes what a document says (line items, rounds, detail rows,
// Thai texts) and this one lays it out with signintech/gopdf and xuri/excelize. Nothing here reads a clock, a database
// or a network: every instant, row, image and font is an input, so a statement renders the same bytes' content every
// time (a re-download serves the stored file anyway).
//
// The PDFs keep the browser's layout in millimetres on A4 (jsPDF units "mm"): the same coordinates, font sizes and
// table geometry (jspdf-autotable's defaults: cell padding 5 pt, line height 1.15, striped or grid themes, header
// repeated on every page). Text has no GPOS shaping, as with jsPDF; Thai marks keep the glyph positions of the font.
// Every text a PDF draws is also recorded as an Op, which is what the tests pin (the PDF's own content stream holds
// subset glyph ids, not text).
package render

import (
	"bytes"
	"embed"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/signintech/gopdf"
)

//go:embed fonts/Sarabun-Regular.ttf fonts/Sarabun-Bold.ttf
var embedded embed.FS

// Fonts are the Sarabun faces the PDFs use (web:public/fonts; SIL Open Font License 1.1, see fonts/README.md).
type Fonts struct {
	Regular, Bold []byte
}

// FontFiles are the file names LoadFonts reads from PDF_FONT_DIR.
var FontFiles = [2]string{"Sarabun-Regular.ttf", "Sarabun-Bold.ttf"}

// LoadFonts reads Sarabun Regular and Bold from dir (PDF_FONT_DIR), or the copies built into the binary when dir is
// empty. A missing or unreadable file names the variable, never its contents.
func LoadFonts(dir string) (Fonts, error) {
	read := func(name string) ([]byte, error) { return embedded.ReadFile("fonts/" + name) }
	if strings.TrimSpace(dir) != "" {
		read = func(name string) ([]byte, error) { return os.ReadFile(filepath.Join(dir, name)) }
	}
	reg, err := read(FontFiles[0])
	if err != nil {
		return Fonts{}, fmt.Errorf("PDF_FONT_DIR: cannot read %s", FontFiles[0])
	}
	bold, err := read(FontFiles[1])
	if err != nil {
		return Fonts{}, fmt.Errorf("PDF_FONT_DIR: cannot read %s", FontFiles[1])
	}
	f := Fonts{Regular: reg, Bold: bold}
	if _, err := newPDF(f, time.Time{}); err != nil {
		return Fonts{}, fmt.Errorf("PDF_FONT_DIR: %s and %s are not usable TrueType fonts", FontFiles[0], FontFiles[1])
	}
	return f, nil
}

// Page geometry of A4 portrait in millimetres, and jsPDF's scale factor (points per mm).
const (
	pageW = 210.0
	pageH = 297.0
	k     = 72 / 25.4
)

// autoTable defaults (jspdf-autotable 3.x): a cell padding of 5 pt, the physical line height of a text line and the
// page margin of 40 pt.
const (
	defaultPadding = 5 / k
	lineFactor     = 1.15
	tableMargin    = 40 / k
)

// RGB is a colour.
type RGB [3]uint8

var (
	black = RGB{0, 0, 0}
	white = RGB{255, 255, 255}
)

// Op is one text a PDF draws: what the tests read instead of the content stream.
type Op struct {
	Page  int     // 1-based
	X, Y  float64 // mm; Y is the baseline
	Text  string
	Bold  bool
	Size  float64 // pt
	Align string  // left, center, right
	Color RGB
}

// Image is an image a PDF embeds: JPEG or PNG bytes and the pixel size.
type Image struct {
	Data []byte
	W, H int
}

// pdfDoc is a gopdf document with jsPDF's text model: a current font (Sarabun regular or bold, size in points), a
// current text colour, text drawn at a baseline with an alignment, and the recorded Ops.
type pdfDoc struct {
	pdf   *gopdf.GoPdf
	page  int
	bold  bool
	size  float64
	color RGB
	ops   []Op
	err   error
}

const (
	familyRegular = "sarabun"
	familyBold    = "sarabun-bold"
)

func newPDF(f Fonts, created time.Time) (*pdfDoc, error) {
	p := &gopdf.GoPdf{}
	p.Start(gopdf.Config{PageSize: gopdf.Rect{W: pageW, H: pageH}, Unit: gopdf.UnitMM})
	if err := p.AddTTFFontData(familyRegular, f.Regular); err != nil {
		return nil, err
	}
	if err := p.AddTTFFontData(familyBold, f.Bold); err != nil {
		return nil, err
	}
	p.SetInfo(gopdf.PdfInfo{Producer: "LogiTrack", Creator: "LogiTrack documents.render", CreationDate: created})
	d := &pdfDoc{pdf: p, size: 16}
	d.addPage()
	d.setFont(false, 16)
	return d, d.err
}

func (d *pdfDoc) fail(err error) {
	if err != nil && d.err == nil {
		d.err = err
	}
}

func (d *pdfDoc) addPage() {
	d.pdf.AddPage()
	d.page++
	// A new page starts with the default state in gopdf: restore the current font and colours.
	if d.page > 1 {
		d.setFont(d.bold, d.size)
		d.setTextColor(d.color)
	}
	d.pdf.SetLineWidth(0.200025) // jsPDF's default line width (0.567 pt)
	d.pdf.SetStrokeColor(0, 0, 0)
}

// pages is the number of pages so far.
func (d *pdfDoc) pages() int { return d.page }

// setPage moves drawing to page n (1-based), for the page numbers drawn at the end.
func (d *pdfDoc) setPage(n int) {
	d.fail(d.pdf.SetPage(n))
	d.page = n
}

func (d *pdfDoc) setFont(bold bool, size float64) {
	d.bold, d.size = bold, size
	family := familyRegular
	if bold {
		family = familyBold
	}
	d.fail(d.pdf.SetFont(family, "", size))
}

func (d *pdfDoc) setTextColor(c RGB) {
	d.color = c
	d.pdf.SetTextColor(c[0], c[1], c[2])
}

// width is the width of s in the current font, in mm.
func (d *pdfDoc) width(s string) float64 {
	if s == "" {
		return 0
	}
	w, err := d.pdf.MeasureTextWidth(s)
	d.fail(err)
	return w
}

// lineHeight is the distance between two lines of the current font (jsPDF: fontSize * 1.15 / k).
func (d *pdfDoc) lineHeight() float64 { return d.size * lineFactor / k }

// text draws s with its baseline at y, starting at x (left), centred on x or ending at x (right), as jsPDF's
// doc.text(s, x, y, {align}).
func (d *pdfDoc) text(s string, x, y float64, align string) {
	if align == "" {
		align = "left"
	}
	d.ops = append(d.ops, Op{Page: d.page, X: x, Y: y, Text: s, Bold: d.bold, Size: d.size, Align: align, Color: d.color})
	if s == "" {
		return
	}
	switch align {
	case "center":
		x -= d.width(s) / 2
	case "right":
		x -= d.width(s)
	}
	d.pdf.SetXY(x, y)
	d.fail(d.pdf.Text(s))
}

// lines draws several lines from baseline y down (jsPDF doc.text(string[], x, y)).
func (d *pdfDoc) lines(ls []string, x, y float64) {
	for i, l := range ls {
		d.text(l, x, y+float64(i)*d.lineHeight(), "left")
	}
}

func (d *pdfDoc) line(x1, y1, x2, y2 float64) { d.pdf.Line(x1, y1, x2, y2) }

// rect draws an outlined rectangle from its upper-left corner (jsPDF doc.rect).
func (d *pdfDoc) rect(x, y, w, h float64) { d.pdf.RectFromUpperLeftWithStyle(x, y, w, h, "D") }

// fillRect fills a rectangle with c.
func (d *pdfDoc) fillRect(x, y, w, h float64, c RGB) {
	d.pdf.SetFillColor(c[0], c[1], c[2])
	d.pdf.RectFromUpperLeftWithStyle(x, y, w, h, "F")
}

// image draws img at (x, y) scaled to w x h mm; an image gopdf cannot read is skipped, as the browser skipped a corrupt
// one.
func (d *pdfDoc) image(img Image, x, y, w, h float64) bool {
	holder, err := gopdf.ImageHolderByBytes(img.Data)
	if err != nil {
		return false
	}
	return d.pdf.ImageByHolder(holder, x, y, &gopdf.Rect{W: w, H: h}) == nil
}

func (d *pdfDoc) bytes() ([]byte, error) {
	if d.err != nil {
		return nil, d.err
	}
	var b bytes.Buffer
	if _, err := d.pdf.WriteTo(&b); err != nil {
		return nil, err
	}
	return b.Bytes(), nil
}

// split wraps text to maxW mm in the current font (jsPDF splitTextToSize): paragraphs on newlines, words on spaces, a
// word wider than the line broken between characters (a Thai sentence has no spaces), combining marks kept with their
// base character.
func (d *pdfDoc) split(text string, maxW float64) []string {
	var out []string
	for _, para := range strings.Split(strings.ReplaceAll(text, "\r\n", "\n"), "\n") {
		words := strings.Split(para, " ")
		line := ""
		flush := func() {
			out = append(out, line)
			line = ""
		}
		for _, w := range words {
			candidate := w
			if line != "" {
				candidate = line + " " + w
			}
			if d.width(candidate) <= maxW {
				line = candidate
				continue
			}
			if line != "" {
				flush()
			}
			if d.width(w) <= maxW {
				line = w
				continue
			}
			for _, cluster := range clusters(w) { // break a long word between characters
				if line != "" && d.width(line+cluster) > maxW {
					flush()
				}
				line += cluster
			}
		}
		flush()
	}
	return out
}

// clusters splits s into a base character followed by its combining marks (Thai vowels above and below, tone marks).
func clusters(s string) []string {
	var out []string
	for len(s) > 0 {
		_, n := utf8.DecodeRuneInString(s)
		for n < len(s) {
			r, m := utf8.DecodeRuneInString(s[n:])
			if !unicode.Is(unicode.Mn, r) {
				break
			}
			n += m
		}
		out = append(out, s[:n])
		s = s[n:]
	}
	return out
}

// --- tables (jspdf-autotable) ------------------------------------------------------------------------------------

// column fixes a table column: a width in mm (0 = automatic) and the horizontal alignment of its cells.
type column struct {
	width float64
	align string
}

// tableStyle is what a call of autoTable sets: the font size of head and body, the cell padding, the theme (striped:
// alternate rows filled 245; grid: every cell outlined) and the head's fill and text colours.
type tableStyle struct {
	fontSize float64
	padding  float64
	grid     bool
	headFill RGB
	headText RGB
	bodyText RGB
	marginL  float64
	marginR  float64
}

// table draws head and body from startY, breaking pages as autoTable does (the head repeats on every page, a row never
// splits across pages), and returns the y below the last row (lastAutoTable.finalY).
func (d *pdfDoc) table(startY float64, head []string, body [][]string, cols map[int]column, st tableStyle) float64 {
	if st.padding == 0 {
		st.padding = defaultPadding
	}
	if st.marginL == 0 {
		st.marginL = tableMargin
	}
	if st.marginR == 0 {
		st.marginR = tableMargin
	}
	n := len(head)
	tableW := pageW - st.marginL - st.marginR
	widths := d.columnWidths(head, body, cols, st, tableW)
	textW := func(i int) float64 { return widths[i] - 2*st.padding }

	wrap := func(row []string, bold bool) ([][]string, float64) {
		d.setFont(bold, st.fontSize)
		cells := make([][]string, n)
		maxLines := 1
		for i := 0; i < n && i < len(row); i++ {
			cells[i] = d.split(row[i], textW(i))
			if len(cells[i]) > maxLines {
				maxLines = len(cells[i])
			}
		}
		return cells, float64(maxLines)*d.lineHeight() + 2*st.padding
	}
	draw := func(cells [][]string, y, h float64, bold bool, fill *RGB, text RGB) {
		x := st.marginL
		for i := range n {
			if fill != nil {
				d.fillRect(x, y, widths[i], h, *fill)
			}
			if st.grid {
				d.pdf.SetLineWidth(0.1)
				d.pdf.SetStrokeColor(80, 80, 80)
				d.rect(x, y, widths[i], h)
				d.pdf.SetLineWidth(0.200025)
				d.pdf.SetStrokeColor(0, 0, 0)
			}
			d.setFont(bold, st.fontSize)
			d.setTextColor(text)
			align := cols[i].align
			tx := x + st.padding
			switch align {
			case "center":
				tx = x + widths[i]/2
			case "right":
				tx = x + widths[i] - st.padding
			}
			// autoTableText: the first baseline sits fontSize * (2 - 1.15) below the padded top.
			base := y + st.padding + st.fontSize/k*(2-lineFactor)
			for j, l := range cells[i] {
				d.text(l, tx, base+float64(j)*d.lineHeight(), align)
			}
			x += widths[i]
		}
		d.setTextColor(black)
	}
	headCells, headH := wrap(head, true)
	bottom := pageH - tableMargin
	y := startY
	drawHead := func() {
		fill := st.headFill
		draw(headCells, y, headH, true, &fill, st.headText)
		y += headH
	}
	drawHead()
	for r, row := range body {
		cells, h := wrap(row, false)
		if y+h > bottom {
			d.addPage()
			y = tableMargin
			drawHead()
		}
		var fill *RGB
		if !st.grid && r%2 == 1 {
			f := RGB{245, 245, 245}
			fill = &f
		}
		draw(cells, y, h, false, fill, st.bodyText)
		y += h
	}
	return y
}

// columnWidths gives the fixed columns their width and shares the rest of the table among the automatic ones in
// proportion to their widest cell (autoTable's resize of auto columns).
func (d *pdfDoc) columnWidths(head []string, body [][]string, cols map[int]column, st tableStyle, tableW float64) []float64 {
	n := len(head)
	widths := make([]float64, n)
	fixed := 0.0
	var auto []int
	natural := make([]float64, n)
	for i := range n {
		if c, ok := cols[i]; ok && c.width > 0 {
			widths[i] = c.width
			fixed += c.width
			continue
		}
		auto = append(auto, i)
		d.setFont(true, st.fontSize)
		w := d.width(head[i])
		d.setFont(false, st.fontSize)
		for _, row := range body {
			if i < len(row) {
				w = max(w, d.width(row[i]))
			}
		}
		natural[i] = w + 2*st.padding
	}
	rest := tableW - fixed
	if len(auto) == 0 || rest <= 0 {
		return widths
	}
	sum := 0.0
	for _, i := range auto {
		sum += natural[i]
	}
	for _, i := range auto {
		if sum <= 0 {
			widths[i] = rest / float64(len(auto))
			continue
		}
		widths[i] = rest * natural[i] / sum
	}
	return widths
}

// errNoFonts is the render error of a renderer built without fonts.
var errNoFonts = errors.New("render: no fonts (PDF_FONT_DIR)")
