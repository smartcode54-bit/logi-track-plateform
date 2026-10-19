//go:build integration

package drivers_test

import (
	"bytes"
	"github.com/google/uuid"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/auth"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/storage"
	"strconv"
	"strings"
	"testing"
)

func TestSupportDocumentBrokerRealSessionConsentAndRange(t *testing.T) {
	w := newWorldWith(t, auth.Firebase{}, true)
	w.exec(`SELECT set_config('app.role','system',false),set_config('app.bypass_tenant','on',false)`)
	driver := w.driver(w.O, "PRIVATE_DOCUMENT_PERSON")
	body := []byte("%PDF-1.7\nPRIVATE_DOCUMENT_BODY\n%%EOF")
	key := "drivers/" + driver + "/license-" + uuid.NewString() + ".pdf"
	info, err := w.signing.Local().Write(key, bytes.NewReader(body), "application/pdf", 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	file := w.id(`INSERT INTO file_objects(bucket,object_key,tenant_id,purpose,owner_kind,owner_id,status,committed_at,storage_backend,content_type,size_bytes,sha256)VALUES('logitrack',$1,$2,'driver_license','driver',$3,'committed',now(),'local','application/pdf',$4,$5)RETURNING id::text`, key, w.O, driver, len(body), info.SHA256)
	w.exec(`UPDATE drivers SET license_file_id=$2 WHERE id=$1`, driver, file)
	login := w.post("/v1/auth/login", "", map[string]any{"email": "pa@example.test", "password": pw, "platform": "web"})
	expect(t, login, 200, "")
	support := w.supportVisit(w.O, str(login.data()["accessToken"]))
	path := "/v1/support/files/" + file + "/content"
	expect(t, w.get(path, support), 403, "support_access_masked")
	expect(t, w.get("/v1/support/files/"+uuid.NewString()+"/content", support), 403, "support_access_masked")
	w.exec(`UPDATE tenants SET support_access_enabled=true,support_access_version=support_access_version+1,support_access_changed_at=now(),support_access_changed_by=$2 WHERE id=$1`, w.O, w.pa)
	expect(t, w.get(path, support), 401, "token_expired")
	rotated := w.post("/v1/auth/refresh", "", map[string]any{"refreshToken": login.data()["refreshToken"]})
	expect(t, rotated, 200, "")
	support = str(rotated.data()["accessToken"])
	for _, endpoint := range []string{path, "/v1/drivers/" + driver + "/documents/license"} {
		got := w.get(endpoint, support)
		expect(t, got, 200, "")
		if !bytes.Equal(got.raw, body) {
			t.Fatalf("body mismatch %s", got.raw)
		}
		for k, v := range map[string]string{"Content-Type": "application/pdf", "Cache-Control": "private, no-store", "Content-Security-Policy": "sandbox; default-src 'none'", "X-Content-Type-Options": "nosniff", "Content-Disposition": "attachment; filename=\"document.pdf\""} {
			if got.header.Get(k) != v {
				t.Fatal(k, got.header)
			}
		}
		for _, name := range []string{"Location", "ETag", "Last-Modified"} {
			if got.header.Get(name) != "" {
				t.Fatal(name, got.header)
			}
		}
	}
	got := w.do("GET", path, support, nil, "Range", "bytes=0-3", "If-None-Match", "anything")
	expect(t, got, 206, "")
	if string(got.raw) != "%PDF" || got.header.Get("Content-Range") != "bytes 0-3/"+strconv.Itoa(len(body)) {
		t.Fatal(got.header, string(got.raw))
	}
	head := w.do("HEAD", path, support, nil)
	expect(t, head, 200, "")
	if len(head.raw) != 0 || head.header.Get("Content-Length") != strconv.Itoa(len(body)) {
		t.Fatal(head.header, string(head.raw))
	}
	detail := w.get("/v1/drivers/"+driver, support)
	expect(t, detail, 200, "")
	if detail.data()["licenseUrl"] != storage.SupportFilePath(uuid.MustParse(file)) || detail.data()["licenseKey"] != nil {
		t.Fatal(string(detail.raw))
	}
	// Same selected tenant and owner claim are insufficient without its current reference.
	w.exec(`UPDATE drivers SET license_file_id=NULL WHERE id=$1`, driver)
	expect(t, w.get(path, support), 404, "not_found")
	w.exec(`UPDATE drivers SET license_file_id=$2 WHERE id=$1`, driver, file)
	w.exec(`UPDATE file_objects SET tenant_id=$2 WHERE id=$1`, file, w.A)
	expect(t, w.get(path, support), 404, "not_found")
	w.exec(`UPDATE file_objects SET tenant_id=$2,purpose='report',owner_kind='report' WHERE id=$1`, file, w.O)
	expect(t, w.get(path, support), 404, "not_found")
	w.exec(`UPDATE file_objects SET purpose='driver_license',owner_kind='driver' WHERE id=$1`, file)
	w.exec(`UPDATE tenants SET support_access_enabled=false,support_access_version=support_access_version+1,support_access_changed_at=now(),support_access_changed_by=$2 WHERE id=$1`, w.O, w.pa)
	revoked := w.get(path, support)
	expect(t, revoked, 401, "token_expired")
	if strings.Contains(string(revoked.raw), "PRIVATE_DOCUMENT_BODY") {
		t.Fatal("revoked disclosure")
	}
	if w.signing.calls.Load() != 0 {
		t.Fatal("broker called signer")
	}
}
