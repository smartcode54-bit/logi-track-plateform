// Package email sends mail over SMTP for the notify.email consumer (main spec §7.6): SMTP_HOST,
// SMTP_PORT, SMTP_USER, SMTP_PASSWORD, SMTP_FROM, SMTP_FROM_NAME, SMTP_STARTTLS; mailpit locally.
// Messages are multipart/alternative (text and HTML, UTF-8, quoted-printable) so Thai text survives
// every client. Errors never carry the password or the message body.
package email

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/tls"
	"encoding/hex"
	"errors"
	"fmt"
	"mime"
	"mime/multipart"
	"mime/quotedprintable"
	"net"
	"net/mail"
	"net/smtp"
	"net/textproto"
	"strconv"
	"strings"
	"time"
)

// Message is one mail to one recipient.
type Message struct {
	To      string // a bare address; the consumer picks it server-side
	Subject string
	Text    string
	HTML    string
}

// Sender delivers a Message.
type Sender interface {
	Send(ctx context.Context, m Message) error
}

// Config is the SMTP connection (main spec §16.1).
type Config struct {
	Host     string
	Port     int
	User     string
	Password string
	From     string // envelope and header sender address
	FromName string
	StartTLS bool          // require STARTTLS before AUTH and DATA
	Timeout  time.Duration // whole conversation; 0 = 30 s
}

// SMTP is a Sender that opens one connection per message.
type SMTP struct {
	cfg  Config
	from mail.Address
}

// New validates cfg.
func New(cfg Config) (*SMTP, error) {
	if cfg.Host == "" || cfg.Port <= 0 || cfg.Port > 65535 {
		return nil, errors.New("email: SMTP_HOST and SMTP_PORT are required")
	}
	from, err := mail.ParseAddress(cfg.From)
	if err != nil || from.Name != "" {
		return nil, errors.New("email: SMTP_FROM must be a bare email address")
	}
	if strings.ContainsAny(cfg.FromName, "\r\n") {
		return nil, errors.New("email: SMTP_FROM_NAME must be one line")
	}
	if cfg.Timeout <= 0 {
		cfg.Timeout = 30 * time.Second
	}
	return &SMTP{cfg: cfg, from: mail.Address{Name: cfg.FromName, Address: from.Address}}, nil
}

// PermanentError is an SMTP 5xx reply (or a recipient that can never be valid): retrying cannot help.
type PermanentError struct{ Err error }

func (e *PermanentError) Error() string { return "email: permanent failure: " + e.Err.Error() }
func (e *PermanentError) Unwrap() error { return e.Err }

// IsPermanent reports whether err is a PermanentError.
func IsPermanent(err error) bool {
	var p *PermanentError
	return errors.As(err, &p)
}

// Send delivers m. A 5xx reply is a *PermanentError; network failures and 4xx replies are transient.
func (s *SMTP) Send(ctx context.Context, m Message) error {
	to, err := mail.ParseAddress(m.To)
	if err != nil {
		return &PermanentError{Err: errors.New("recipient is not an email address")}
	}
	body, err := s.compose(to.Address, m)
	if err != nil {
		return &PermanentError{Err: err}
	}
	ctx, cancel := context.WithTimeout(ctx, s.cfg.Timeout)
	defer cancel()
	addr := net.JoinHostPort(s.cfg.Host, strconv.Itoa(s.cfg.Port))
	var d net.Dialer
	conn, err := d.DialContext(ctx, "tcp", addr)
	if err != nil {
		return fmt.Errorf("email: connect: %w", err)
	}
	deadline, _ := ctx.Deadline()
	_ = conn.SetDeadline(deadline)
	c, err := smtp.NewClient(conn, s.cfg.Host)
	if err != nil {
		_ = conn.Close()
		return classify("greeting", err)
	}
	defer func() { _ = c.Close() }()
	if s.cfg.StartTLS {
		if ok, _ := c.Extension("STARTTLS"); !ok {
			return errors.New("email: the server does not offer STARTTLS (SMTP_STARTTLS=true)")
		}
		if err := c.StartTLS(&tls.Config{ServerName: s.cfg.Host, MinVersion: tls.VersionTLS12}); err != nil {
			return classify("starttls", err)
		}
	}
	if s.cfg.User != "" {
		// PlainAuth refuses to send the password over an unencrypted connection to a remote host.
		if err := c.Auth(smtp.PlainAuth("", s.cfg.User, s.cfg.Password, s.cfg.Host)); err != nil {
			return classify("auth", err)
		}
	}
	if err := c.Mail(s.from.Address); err != nil {
		return classify("mail from", err)
	}
	if err := c.Rcpt(to.Address); err != nil {
		return classify("rcpt to", err)
	}
	w, err := c.Data()
	if err != nil {
		return classify("data", err)
	}
	if _, err := w.Write(body); err != nil {
		return fmt.Errorf("email: write: %w", err)
	}
	if err := w.Close(); err != nil {
		return classify("data end", err)
	}
	_ = c.Quit()
	return nil
}

// classify turns an SMTP reply into a permanent (5xx) or transient error; the reply text is kept.
func classify(step string, err error) error {
	var tp *textproto.Error
	if errors.As(err, &tp) && tp.Code >= 500 {
		return &PermanentError{Err: fmt.Errorf("%s: %d %s", step, tp.Code, tp.Msg)}
	}
	return fmt.Errorf("email: %s: %w", step, err)
}

// compose renders the RFC 5322 message: encoded headers and a multipart/alternative body.
func (s *SMTP) compose(to string, m Message) ([]byte, error) {
	if strings.ContainsAny(m.Subject, "\r\n") {
		return nil, errors.New("subject must be one line")
	}
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	id := make([]byte, 16)
	_, _ = rand.Read(id)
	domain := s.from.Address[strings.LastIndexByte(s.from.Address, '@')+1:]
	hdr := []string{
		"From: " + s.from.String(),
		"To: " + to,
		"Subject: " + mime.BEncoding.Encode("UTF-8", m.Subject),
		"Date: " + time.Now().UTC().Format(time.RFC1123Z),
		"Message-ID: <" + hex.EncodeToString(id) + "@" + domain + ">",
		"MIME-Version: 1.0",
		"Auto-Submitted: auto-generated",
		"Content-Type: multipart/alternative; boundary=" + mw.Boundary(),
	}
	var out bytes.Buffer
	out.WriteString(strings.Join(hdr, "\r\n") + "\r\n\r\n")
	for _, part := range []struct{ ctype, body string }{
		{"text/plain; charset=UTF-8", m.Text},
		{"text/html; charset=UTF-8", m.HTML},
	} {
		if part.body == "" {
			continue
		}
		pw, err := mw.CreatePart(textproto.MIMEHeader{
			"Content-Type":              {part.ctype},
			"Content-Transfer-Encoding": {"quoted-printable"},
		})
		if err != nil {
			return nil, err
		}
		qp := quotedprintable.NewWriter(pw)
		if _, err := qp.Write([]byte(strings.ReplaceAll(part.body, "\n", "\r\n"))); err != nil {
			return nil, err
		}
		if err := qp.Close(); err != nil {
			return nil, err
		}
	}
	if err := mw.Close(); err != nil {
		return nil, err
	}
	out.Write(buf.Bytes())
	return out.Bytes(), nil
}
