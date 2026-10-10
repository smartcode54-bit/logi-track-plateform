package notify

import (
	"strings"
	"testing"
	"time"
)

func TestTemplatesRenderEveryPurposeAndLocale(t *testing.T) {
	link := ResetLink("https://web.example/", "TOKEN")
	if link != "https://web.example/reset-password#token=TOKEN" {
		t.Fatalf("link %s", link)
	}
	for _, c := range []struct{ purpose, locale, subject, text string }{
		{PurposeReset, "th", "ตั้งรหัสผ่านใหม่สำหรับ LogiTrack", "30 นาที"},
		{PurposeReset, "en", "Reset your LogiTrack password", "30 minutes"},
		{PurposeInvite, "th", "เชิญเข้าใช้งาน LogiTrack", "72 ชั่วโมง"},
		{PurposeInvite, "en", "You are invited to LogiTrack", "72 hours"},
		{PurposeReset, "fr", "ตั้งรหัสผ่านใหม่สำหรับ LogiTrack", "30 นาที"}, // unknown locale -> Thai
	} {
		ttl := 30 * time.Minute
		if c.purpose == PurposeInvite {
			ttl = InviteTTL
		}
		locale := c.locale
		if locale != LocaleEN {
			locale = LocaleTH
		}
		m, err := renderLink(c.purpose, c.locale, "a@b.test", linkData{Name: "<b>Somchai</b>", Link: link, ValidFor: validFor(ttl, locale)})
		if err != nil {
			t.Fatal(err)
		}
		if m.Subject != c.subject || !strings.Contains(m.Text, c.text) || !strings.Contains(m.Text, link) || m.To != "a@b.test" {
			t.Fatalf("%s/%s: subject %q text %q", c.purpose, c.locale, m.Subject, m.Text)
		}
		if !strings.Contains(m.HTML, `href="https://web.example/reset-password#token=TOKEN"`) || strings.Contains(m.HTML, "<b>Somchai") {
			t.Fatalf("%s/%s html must link the token and escape the name:\n%s", c.purpose, c.locale, m.HTML)
		}
		if strings.Contains(strings.ToLower(m.Text+m.HTML), "password:") {
			t.Fatal("a mail must never carry a password (R29)")
		}
	}
}

func TestValidFor(t *testing.T) {
	for d, want := range map[time.Duration][2]string{
		30 * time.Minute: {"30 minutes", "30 นาที"},
		90 * time.Minute: {"90 minutes", "90 นาที"},
		72 * time.Hour:   {"72 hours", "72 ชั่วโมง"},
	} {
		if en, th := validFor(d, LocaleEN), validFor(d, LocaleTH); en != want[0] || th != want[1] {
			t.Fatalf("%v: %q %q", d, en, th)
		}
	}
}
