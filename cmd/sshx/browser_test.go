package main

import "testing"

func TestBrowserLauncherRejectsNonlocalURLs(t *testing.T) {
	for _, raw := range []string{"https://127.0.0.1/", "http://example.com/", "file:///etc/passwd", "http://user@127.0.0.1/", "http://127.0.0.1.example.com/", "http://0.0.0.0/", "not a URL"} {
		if launchBrowser(raw) == nil {
			t.Errorf("accepted %q", raw)
		}
	}
}
