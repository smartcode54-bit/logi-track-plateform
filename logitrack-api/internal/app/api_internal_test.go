package app

import "testing"

func TestRequestTarget(t *testing.T) {
	for in, want := range map[string][2]string{
		"/media/a/b.jpg?X-LT-Expires=1":       {"/media/a/b.jpg", "X-LT-Expires=1"},
		"/v1/uploads/local/x%2Fy.jpg#frag":    {"/v1/uploads/local/x%2Fy.jpg", ""},
		"/v1/uploads/local/a.jpg?x=1#frag":    {"/v1/uploads/local/a.jpg", "x=1"},
		"http://api.test/media/a.jpg?x=1":     {"/media/a.jpg", "x=1"},
		"https://api.test":                    {"/", ""},
		"https://api.test?x=1":                {"/", "x=1"},
		"/v1/me?next=http://evil.test/media/": {"/v1/me", "next=http://evil.test/media/"},
		"*":                                   {"*", ""},
	} {
		if p, q := requestTarget([]byte(in)); p != want[0] || q != want[1] {
			t.Errorf("requestTarget(%q) = %q, %q, want %q", in, p, q, want)
		}
	}
}
