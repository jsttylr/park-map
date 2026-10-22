package main

import (
	"crypto/subtle"
	"encoding/json"
	"net/http"
	"strconv"

	"github.com/jsttylr/park-map/internal/gateway"
)

// adminOnly asks for the admin password, if one is set, before the settings
// and log can be seen or changed.
func adminOnly(pass string, h http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if pass != "" {
			_, p, ok := r.BasicAuth()
			if !ok || subtle.ConstantTimeCompare([]byte(p), []byte(pass)) != 1 {
				w.Header().Set("WWW-Authenticate", `Basic realm="park-map settings"`)
				http.Error(w, "password needed", http.StatusUnauthorized)
				return
			}
		}
		h(w, r)
	}
}

func gatewayRoutes(mux *http.ServeMux, g *gateway.Gateway, pass string, settingsPage []byte) {
	mux.HandleFunc("GET /settings", adminOnly(pass, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Write(settingsPage)
	}))
	mux.HandleFunc("GET /api/settings", adminOnly(pass, func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, map[string]any{"settings": g.Store().Get().Public(), "protected": pass != ""})
	}))
	mux.HandleFunc("PUT /api/settings", adminOnly(pass, func(w http.ResponseWriter, r *http.Request) {
		var s gateway.Settings
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&s); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		if err := g.Store().Update(s); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		g.Log().Info("Settings saved")
		g.CheckNow()
		writeJSON(w, map[string]any{"settings": g.Store().Get().Public(), "protected": pass != ""})
	}))
	mux.HandleFunc("GET /api/gateway/log", adminOnly(pass, func(w http.ResponseWriter, r *http.Request) {
		after, _ := strconv.Atoi(r.URL.Query().Get("after"))
		writeJSON(w, g.Log().Since(after))
	}))
	mux.HandleFunc("POST /api/gateway/check", adminOnly(pass, func(w http.ResponseWriter, r *http.Request) {
		g.CheckNow()
		writeJSON(w, map[string]string{"text": "Checking the mailbox now. Watch the log."})
	}))
	mux.HandleFunc("POST /api/gateway/test-mail", adminOnly(pass, func(w http.ResponseWriter, r *http.Request) {
		text, err := g.TestMailbox(g.Store().Get())
		if err != nil {
			g.Log().Error("Mailbox test failed", err.Error())
			writeJSON(w, map[string]string{"error": err.Error()})
			return
		}
		g.Log().Info("Mailbox test", text)
		writeJSON(w, map[string]string{"text": text})
	}))
	// Try a message from the settings page. Nothing is sent unless asked.
	mux.HandleFunc("POST /api/gateway/try", adminOnly(pass, func(w http.ResponseWriter, r *http.Request) {
		var in struct {
			Text string   `json:"text"`
			Lat  *float64 `json:"lat"`
			Lon  *float64 `json:"lon"`
			Live bool     `json:"live"` // really post spots
		}
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<16)).Decode(&in); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		s := g.Store().Get()
		s.TestMode = s.TestMode || !in.Live
		m := &gateway.Message{From: "settings page", Text: in.Text}
		if in.Lat != nil && in.Lon != nil {
			m.Lat, m.Lon, m.HasPos = *in.Lat, *in.Lon, true
		}
		g.Log().Info("Trying a message from the settings page", "text: %q, test mode: %v", in.Text, s.TestMode)
		reply := g.Answer(s, m)
		g.Log().Info("Reply would be", reply)
		writeJSON(w, map[string]any{"reply": reply, "chars": len(reply)})
	}))
}
