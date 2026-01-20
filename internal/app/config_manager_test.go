package app

import "testing"

func TestNormalizeDomainHostPath_StrictValidation(t *testing.T) {
	t.Run("valid hosts", func(t *testing.T) {
		cases := []struct {
			host string
			path string
		}{
			{"a.example.com", "/"},
			{"A.Example.Com", "/foo"},    // host lowercased
			{"xn--fiqs8s.example", "/x"}, // punycode ok
		}
		for _, c := range cases {
			_, _, err := normalizeDomainHostPath(c.host, c.path)
			if err != nil {
				t.Fatalf("expected ok for host=%q path=%q, got err=%v", c.host, c.path, err)
			}
		}
	})

	t.Run("invalid hosts", func(t *testing.T) {
		bad := []string{
			"",
			"NULL",
			"http://a.example.com",
			"a.example.com:8080",
			"*.example.com", // wildcard forbidden
			"1.2.3.4",       // ipv4 forbidden
			"2001:db8::1",   // ipv6 forbidden
			"localhost",     // must contain dot
			"a_example.com", // underscore
			"-a.example.com",
			"a-.example.com",
			"a..example.com",
			"*.*.com",
		}
		for _, h := range bad {
			_, _, err := normalizeDomainHostPath(h, "/")
			if err == nil {
				t.Fatalf("expected err for host=%q", h)
			}
		}
	})

	t.Run("invalid paths", func(t *testing.T) {
		cases := []string{
			"abc",     // must start with /
			"/a b",    // no whitespace
			"/a?b",    // no query char
			"/a#b",    // no fragment char
			"/./a",    // no dot segment
			"/a/../b", // no dotdot segment
			"/a/\n/b", // control char
			"/a/\t/b", // whitespace
		}
		for _, p := range cases {
			_, _, err := normalizeDomainHostPath("a.example.com", p)
			if err == nil {
				t.Fatalf("expected err for path=%q", p)
			}
		}
	})

	t.Run("path normalization", func(t *testing.T) {
		h, p, err := normalizeDomainHostPath("A.Example.Com", "/a//b/")
		if err != nil {
			t.Fatalf("unexpected err: %v", err)
		}
		if h != "a.example.com" {
			t.Fatalf("expected host lowercased, got %q", h)
		}
		if p != "/a/b" {
			t.Fatalf("expected path normalized to /a/b, got %q", p)
		}
	})
}
