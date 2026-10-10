package main

import (
	"net/url"
	"strings"
	"testing"
	"time"
)

// The presigned-URL example of the AWS S3 SigV4 documentation ("Authenticating Requests:
// Using Query Parameters"), with its published example credentials.
func TestPresignMatchesAWSExample(t *testing.T) {
	now := time.Date(2013, 5, 24, 0, 0, 0, 0, time.UTC)
	got, err := Presign("https://examplebucket.s3.amazonaws.com", "", "test.txt", "us-east-1",
		"AKIAIOSFODNN7EXAMPLE", "wJalrXUtnFEMI/K7MDENG/bPxRfiCYEXAMPLEKEY", now, 24*time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	const want = "https://examplebucket.s3.amazonaws.com/test.txt" +
		"?X-Amz-Algorithm=AWS4-HMAC-SHA256" +
		"&X-Amz-Credential=AKIAIOSFODNN7EXAMPLE%2F20130524%2Fus-east-1%2Fs3%2Faws4_request" +
		"&X-Amz-Date=20130524T000000Z&X-Amz-Expires=86400&X-Amz-SignedHeaders=host" +
		"&X-Amz-Signature=aeeed9bbccd4d02ee5c0109b86d86835f995330da4c265957d157751f604d404"
	if got != want {
		t.Fatalf("got  %s\nwant %s", got, want)
	}
}

func TestPresignPathStyleKeepsHostAndEncodesKey(t *testing.T) {
	now := time.Date(2026, 10, 10, 0, 0, 0, 0, time.UTC)
	got, err := Presign("http://media.localhost:8443", "logitrack", "probe/a b+c.txt", "us-east-1", "k", "s", now, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	u, err := url.Parse(got)
	if err != nil {
		t.Fatal(err)
	}
	if u.Host != "media.localhost:8443" || u.EscapedPath() != "/logitrack/probe/a%20b%2Bc.txt" {
		t.Fatalf("host %q path %q", u.Host, u.EscapedPath())
	}
	if !strings.Contains(got, "X-Amz-Expires=60&") {
		t.Fatalf("expires not in %s", got)
	}
	for _, bad := range []string{"", "media.localhost", "ftp://x"} {
		if _, err := Presign(bad, "b", "k", "r", "a", "s", now, time.Minute); err == nil {
			t.Errorf("endpoint %q accepted", bad)
		}
	}
}
