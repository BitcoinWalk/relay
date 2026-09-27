package main

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestChooserFixedDestinationAndSecurity(t *testing.T) {
	mux := http.NewServeMux()
	registerChooserAssets(mux)
	registerChatChooser(mux, "/join-chat", chatDestination{"Global <test>", "http://localhost:3343/s/ws%3Alocalhost%3A3342/global-pilot", true})
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, httptest.NewRequest("GET", "/join-chat?next=https://evil.example", nil))
	if w.Code != 200 {
		t.Fatal(w.Code)
	}
	body := w.Body.String()
	for _, part := range []string{"Global &lt;test&gt;", "global-pilot", "Continue in browser", "button disabled", "Local test preview"} {
		if !strings.Contains(body, part) {
			t.Fatalf("missing %s", part)
		}
	}
	if strings.Contains(body, "evil.example") {
		t.Fatal("untrusted redirect target")
	}
	if w.Header().Get("Content-Security-Policy") == "" || w.Header().Get("Referrer-Policy") != "no-referrer" {
		t.Fatal("missing security headers")
	}
	for _, path := range []string{"/chat-chooser/chooser.js", "/chat-chooser/style.css"} {
		out := httptest.NewRecorder()
		mux.ServeHTTP(out, httptest.NewRequest("GET", path, nil))
		if out.Code != 200 || out.Body.Len() == 0 {
			t.Fatal("missing asset", path)
		}
	}
}
