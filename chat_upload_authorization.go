package main

import (
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"

	"fiatjaf.com/nostr"
	"fiatjaf.com/nostr/khatru"
	"github.com/BitcoinWalk/relay/internal/chatstate"
)

const chatUploadAuthorizationPath = "/api/chat/upload-authorization"

var uploadGroup = regexp.MustCompile(`^[A-Za-z0-9_-]{1,128}$`)
var uploadHash = regexp.MustCompile(`^[0-9a-f]{64}$`)

type chatUploadCommand struct {
	Action  string `json:"action"`
	Relay   string `json:"relay"`
	GroupID string `json:"groupId"`
	SHA256  string `json:"sha256"`
	MIME    string `json:"mime"`
	Bytes   int    `json:"bytes"`
}

func uploadRelayOrigin(value string) string {
	u, err := url.Parse(value)
	if err != nil || u.Scheme != "wss" || u.Hostname() == "" || u.Port() != "" || u.User != nil || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" || u.RawPath != "" || (u.Path != "" && u.Path != "/") {
		return ""
	}
	return "wss://" + strings.ToLower(u.Host)
}

func parseChatUpload(e nostr.Event, relay string, now int64) (chatUploadCommand, bool) {
	var c chatUploadCommand
	if e.Kind != 27235 || int64(e.CreatedAt) < now-300 || int64(e.CreatedAt) > now+300 || !e.CheckID() || !e.VerifySignature() || len(e.Tags) != 2 {
		return c, false
	}
	media, action := false, false
	for _, tag := range e.Tags {
		if len(tag) != 2 {
			return c, false
		}
		if tag[0] == "t" && tag[1] == "bitcoinwalk-media" {
			media = true
		}
		if tag[0] == "action" && tag[1] == "upload-chat-image" {
			action = true
		}
	}
	decoder := json.NewDecoder(strings.NewReader(e.Content))
	decoder.DisallowUnknownFields()
	if !media || !action || decoder.Decode(&c) != nil || decoder.Decode(new(any)) != io.EOF {
		return c, false
	}
	origin := uploadRelayOrigin(relay)
	return c, origin != "" && uploadRelayOrigin(c.Relay) == origin && c.Action == "upload-chat-image" && uploadGroup.MatchString(c.GroupID) && uploadHash.MatchString(c.SHA256) && c.Bytes > 0 && c.Bytes <= 5*1024*1024 && (c.MIME == "image/jpeg" || c.MIME == "image/png" || c.MIME == "image/webp")
}

func chatUploadAuthorizationHandler(relay string, checkMember func(string, nostr.PubKey) error) http.Handler {
	limits := &chatLimits{}
	slots := make(chan struct{}, 8)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		if r.Method != http.MethodPost {
			http.Error(w, "POST required", 405)
			return
		}
		if !limits.allow("global", 120, time.Minute, time.Now()) {
			http.Error(w, "Try later", 429)
			return
		}
		select {
		case slots <- struct{}{}:
			defer func() { <-slots }()
		default:
			http.Error(w, "Try later", 503)
			return
		}
		_ = http.NewResponseController(w).SetReadDeadline(time.Now().Add(5 * time.Second))
		defer http.NewResponseController(w).SetReadDeadline(time.Time{})
		r.Body = http.MaxBytesReader(w, r.Body, 16384)
		var event nostr.Event
		decoder := json.NewDecoder(r.Body)
		if decoder.Decode(&event) != nil || decoder.Decode(new(any)) != io.EOF {
			http.Error(w, "Invalid authorization", 400)
			return
		}
		command, valid := parseChatUpload(event, relay, time.Now().Unix())
		if !valid {
			http.Error(w, "Invalid authorization", 401)
			return
		}
		if !limits.allow(event.PubKey.Hex(), 20, time.Minute, time.Now()) {
			http.Error(w, "Try later", 429)
			return
		}
		// An admission-time check, not a reusable grant; no membership result is cached.
		if checkMember(command.GroupID, event.PubKey) != nil {
			http.Error(w, "Upload not authorized", 403)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(struct {
			Allowed bool   `json:"allowed"`
			EventID string `json:"eventId"`
		}{true, event.ID.Hex()})
	})
}

func registerChatUploadAuthorization(relay *khatru.Relay, store *chatstate.Store) {
	relay.Router().Handle(chatUploadAuthorizationPath, chatUploadAuthorizationHandler(relay.ServiceURL, store.CheckMember))
}
