package notify

import (
	"bytes"
	"embed"
	"fmt"
	htmltemplate "html/template"
	"strings"
	texttemplate "text/template"
	"time"

	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/auth"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/email"
)

// Locales of the mail templates; th is the default (the UI's default language).
const (
	LocaleTH = "th"
	LocaleEN = "en"
)

// Purposes of password_reset_tokens and of the mails that carry them.
const (
	PurposeReset  = auth.PurposeReset
	PurposeInvite = auth.PurposeInvite
)

//go:embed templates/*.txt templates/*.html
var templateFS embed.FS

type mailTemplate struct {
	text *texttemplate.Template // blocks "subject" and "text"
	html *htmltemplate.Template // block "html"
}

// templates holds every (purpose, locale) pair; a missing file fails at start-up, not on a send.
var templates = func() map[string]mailTemplate {
	out := map[string]mailTemplate{}
	for _, purpose := range []string{PurposeReset, PurposeInvite} {
		for _, locale := range []string{LocaleTH, LocaleEN} {
			name := purpose + "." + locale
			out[name] = mailTemplate{
				text: texttemplate.Must(texttemplate.ParseFS(templateFS, "templates/"+name+".txt")),
				html: htmltemplate.Must(htmltemplate.ParseFS(templateFS, "templates/"+name+".html")),
			}
		}
	}
	return out
}()

// linkData is what a link mail shows.
type linkData struct {
	Name     string
	Link     string
	ValidFor string
}

// renderLink renders the reset or invite mail in a locale (anything but "en" is Thai).
func renderLink(purpose, locale, to string, d linkData) (email.Message, error) {
	if locale != LocaleEN {
		locale = LocaleTH
	}
	t, ok := templates[purpose+"."+locale]
	if !ok {
		return email.Message{}, fmt.Errorf("notify: no template for %s", purpose)
	}
	var subject, text, html bytes.Buffer
	if err := t.text.ExecuteTemplate(&subject, "subject", d); err != nil {
		return email.Message{}, err
	}
	if err := t.text.ExecuteTemplate(&text, "text", d); err != nil {
		return email.Message{}, err
	}
	if err := t.html.ExecuteTemplate(&html, "html", d); err != nil {
		return email.Message{}, err
	}
	return email.Message{
		To: to, Subject: strings.TrimSpace(subject.String()), Text: strings.TrimLeft(text.String(), "\n"), HTML: html.String(),
	}, nil
}

// validFor renders a link lifetime: minutes below two hours, hours above.
func validFor(d time.Duration, locale string) string {
	if d < 2*time.Hour {
		m := int(d.Round(time.Minute) / time.Minute)
		if locale == LocaleEN {
			return fmt.Sprintf("%d minutes", m)
		}
		return fmt.Sprintf("%d นาที", m)
	}
	h := int(d.Round(time.Hour) / time.Hour)
	if locale == LocaleEN {
		return fmt.Sprintf("%d hours", h)
	}
	return fmt.Sprintf("%d ชั่วโมง", h)
}
