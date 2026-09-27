package main

import (
	"embed"
	"html/template"
	"net/http"
)

//go:embed chat-chooser/*
var chooserAssets embed.FS

var chooserTemplate = template.Must(template.ParseFS(chooserAssets, "chat-chooser/index.html"))

type chatDestination struct {
	Title      string
	BrowserURL string
	Local      bool
}

// Targets come only from provisioned configuration, never query parameters.
// Native opening stays disabled until public-relay device tests pass.
func registerChatChooser(mux *http.ServeMux, route string, destination chatDestination) {
	mux.HandleFunc("GET "+route, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("Referrer-Policy", "no-referrer")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Content-Security-Policy", "default-src 'none'; style-src 'self'; script-src 'self'; base-uri 'none'; frame-ancestors 'none'; form-action 'none'")
		_ = chooserTemplate.Execute(w, destination)
	})
}

func registerChooserAssets(mux *http.ServeMux) {
	for _, name := range []string{"style.css", "chooser.js"} {
		mux.HandleFunc("GET /chat-chooser/"+name, func(w http.ResponseWriter, r *http.Request) {
			data, err := chooserAssets.ReadFile("chat-chooser/" + name)
			if err != nil {
				http.NotFound(w, r)
				return
			}
			mime := "text/css; charset=utf-8"
			if name == "chooser.js" {
				mime = "text/javascript; charset=utf-8"
			}
			w.Header().Set("Content-Type", mime)
			w.Header().Set("X-Content-Type-Options", "nosniff")
			w.Header().Set("Cache-Control", "no-store")
			_, _ = w.Write(data)
		})
	}
}

// Separate preview avoids restarting/resetting the user's active test chat.
func runChooserPreview() error {
	mux := http.NewServeMux()
	registerChooserAssets(mux)
	registerChatChooser(mux, "/join-chat", chatDestination{"Global BitcoinWalk chat", "http://localhost:3343/s/ws%3Alocalhost%3A3342/global-pilot", true})
	registerChatChooser(mux, "/city-test/join-chat", chatDestination{"BitcoinWalk city test chat", "http://localhost:3343/s/ws%3Alocalhost%3A3342/city-pilot", true})
	return (&http.Server{Addr: "127.0.0.1:3344", Handler: mux, ReadHeaderTimeout: 5e9}).ListenAndServe()
}
