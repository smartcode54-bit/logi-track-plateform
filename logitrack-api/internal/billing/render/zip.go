package render

import (
	"archive/zip"
	"bytes"
	"time"
)

// Names of the files inside the bundle (downloadBillingZip, web:lib/billingDocumentRender.ts:655-671); the receipt is
// never bundled (commit 25d5468: it is issued on mark-as-paid).
const (
	BundleInvoiceName = "invoice_summary.pdf"
	BundleDetailName  = "invoice_detail.xlsx"
)

// BundleZIP packs the invoice PDF and the detail sheet (bundle.zip), each file dated at (the statement's generation, so
// a regenerated bundle carries the same dates).
func BundleZIP(invoicePDF, detailXLSX []byte, at time.Time) ([]byte, error) {
	var b bytes.Buffer
	w := zip.NewWriter(&b)
	for _, f := range []struct {
		name string
		body []byte
	}{{BundleInvoiceName, invoicePDF}, {BundleDetailName, detailXLSX}} {
		h := &zip.FileHeader{Name: f.name, Method: zip.Deflate}
		h.Modified = at.UTC()
		fw, err := w.CreateHeader(h)
		if err != nil {
			return nil, err
		}
		if _, err := fw.Write(f.body); err != nil {
			return nil, err
		}
	}
	if err := w.Close(); err != nil {
		return nil, err
	}
	return b.Bytes(), nil
}
