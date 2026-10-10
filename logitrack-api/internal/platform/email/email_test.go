package email

import (
	"bytes"
	"errors"
	"io"
	"mime"
	"mime/multipart"
	"net/mail"
	"net/textproto"
	"strings"
	"testing"
)

func TestNewValidates(t *testing.T) {
	for _, c := range []Config{
		{Port: 25, From: "a@b.test"},
		{Host: "h", Port: 0, From: "a@b.test"},
		{Host: "h", Port: 25, From: "LogiTrack <a@b.test>"},
		{Host: "h", Port: 25, From: "not an address"},
		{Host: "h", Port: 25, From: "a@b.test", FromName: "x\r\nBcc: y@z.test"},
	} {
		if _, err := New(c); err == nil {
			t.Errorf("%+v accepted", c)
		}
	}
}

func TestComposeIsMultipartUTF8(t *testing.T) {
	s, err := New(Config{Host: "mailpit", Port: 1025, From: "no-reply@logitrack.test", FromName: "LogiTrack ขนส่ง"})
	if err != nil {
		t.Fatal(err)
	}
	raw, err := s.compose("somchai@logitrack.test", Message{
		Subject: "ตั้งรหัสผ่านใหม่สำหรับ LogiTrack", Text: "สวัสดี\nบรรทัดสอง", HTML: "<p>สวัสดี</p>",
	})
	if err != nil {
		t.Fatal(err)
	}
	msg, err := mail.ReadMessage(bytes.NewReader(raw))
	if err != nil {
		t.Fatal(err)
	}
	dec := new(mime.WordDecoder)
	if subj, _ := dec.DecodeHeader(msg.Header.Get("Subject")); subj != "ตั้งรหัสผ่านใหม่สำหรับ LogiTrack" {
		t.Fatalf("subject %q", subj)
	}
	from, err := msg.Header.AddressList("From")
	if err != nil || from[0].Name != "LogiTrack ขนส่ง" || from[0].Address != "no-reply@logitrack.test" {
		t.Fatalf("from %v %v", from, err)
	}
	if msg.Header.Get("To") != "somchai@logitrack.test" || msg.Header.Get("Message-Id") == "" || msg.Header.Get("Date") == "" {
		t.Fatalf("headers %v", msg.Header)
	}
	mt, params, err := mime.ParseMediaType(msg.Header.Get("Content-Type"))
	if err != nil || mt != "multipart/alternative" {
		t.Fatalf("content type %s %v", mt, err)
	}
	mr := multipart.NewReader(msg.Body, params["boundary"])
	var parts []string
	for {
		p, err := mr.NextPart() // decodes quoted-printable
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		b, _ := io.ReadAll(p)
		parts = append(parts, p.Header.Get("Content-Type")+"|"+string(b))
	}
	if len(parts) != 2 || parts[0] != "text/plain; charset=UTF-8|สวัสดี\r\nบรรทัดสอง" || parts[1] != "text/html; charset=UTF-8|<p>สวัสดี</p>" {
		t.Fatalf("parts %q", parts)
	}
	for _, line := range strings.Split(string(raw), "\r\n") {
		if len(line) > 998 {
			t.Fatal("a line exceeds RFC 5322's 998 characters")
		}
	}
	if _, err := s.compose("a@b.test", Message{Subject: "x\nBcc: evil@x.test"}); err == nil {
		t.Fatal("a multi-line subject was accepted")
	}
}

func TestClassify(t *testing.T) {
	if err := classify("rcpt to", &textproto.Error{Code: 550, Msg: "no such user"}); !IsPermanent(err) {
		t.Fatalf("550 is %v, want permanent", err)
	}
	if err := classify("rcpt to", &textproto.Error{Code: 451, Msg: "try later"}); IsPermanent(err) {
		t.Fatalf("451 is %v, want transient", err)
	}
	if err := classify("greeting", io.ErrUnexpectedEOF); IsPermanent(err) {
		t.Fatal("a network error is permanent")
	}
}
