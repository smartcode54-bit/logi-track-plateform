package app

import "testing"

func TestRequestPath(t *testing.T) {
	for in, want := range map[string]string{
		"/media/a/b.jpg?X-LT-Expires=1":       "/media/a/b.jpg",
		"/v1/uploads/local/x%2Fy.jpg#frag":    "/v1/uploads/local/x%2Fy.jpg",
		"http://api.test/media/a.jpg?x=1":     "/media/a.jpg",
		"https://api.test":                    "/",
		"/v1/me?next=http://evil.test/media/": "/v1/me",
		"*":                                   "*",
	} {
		if got := requestPath([]byte(in)); got != want {
			t.Errorf("requestPath(%q) = %q, want %q", in, got, want)
		}
	}
}
