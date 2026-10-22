// Package gateway answers satellite messenger (Garmin inReach) messages: it
// checks a mailbox, looks up the parks and summits at the position the
// message was sent from, posts POTA and SOTA spots, and replies.
package gateway

import (
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

// Settings are kept in the config folder, never in the image or the repo.
type Settings struct {
	Enabled  bool   `json:"enabled"`
	TestMode bool   `json:"test_mode"` // log replies and spots instead of sending them
	Callsign string `json:"callsign"`
	// Messages are only accepted from these addresses (or "@domain" for a
	// whole domain), and must contain the PIN if one is set.
	AllowedSenders []string `json:"allowed_senders"`
	PIN            string   `json:"pin"`

	IMAP MailServer `json:"imap"`
	// Interval between mailbox checks, in seconds.
	IntervalSec int    `json:"interval_sec"`
	Folder      string `json:"folder"`
	// SMTP is only used to answer messages sent from ordinary email (handy
	// for testing); inReach messages are answered through Garmin.
	SMTP MailServer `json:"smtp"`

	POTA POTASettings `json:"pota"`
	SOTA SOTASettings `json:"sota"`
}

// MailServer is an IMAP or SMTP server login.
type MailServer struct {
	Host     string `json:"host"`
	Port     int    `json:"port"`
	Security string `json:"security"` // "tls", "starttls" or "none"
	User     string `json:"user"`
	Password string `json:"password,omitempty"`
	HasPass  bool   `json:"has_password"`
}

type POTASettings struct {
	Enabled bool   `json:"enabled"`
	SpotURL string `json:"spot_url"`
}

type SOTASettings struct {
	Enabled  bool   `json:"enabled"`
	User     string `json:"user"`
	Password string `json:"password,omitempty"`
	HasPass  bool   `json:"has_password"`
	TokenURL string `json:"token_url"`
	ClientID string `json:"client_id"`
	SpotURL  string `json:"spot_url"`
}

// Defaults fills in anything left empty.
func (s *Settings) Defaults() {
	if s.IntervalSec == 0 {
		s.IntervalSec = 120
	}
	s.IntervalSec = max(s.IntervalSec, 30)
	if s.Folder == "" {
		s.Folder = "INBOX"
	}
	if s.IMAP.Security == "" {
		s.IMAP.Security = "tls"
	}
	if s.IMAP.Port == 0 {
		s.IMAP.Port = map[string]int{"tls": 993, "starttls": 143, "none": 143}[s.IMAP.Security]
	}
	if s.SMTP.Security == "" {
		s.SMTP.Security = "starttls"
	}
	if s.SMTP.Port == 0 {
		s.SMTP.Port = map[string]int{"tls": 465, "starttls": 587, "none": 25}[s.SMTP.Security]
	}
	if s.POTA.SpotURL == "" {
		s.POTA.SpotURL = "https://api.pota.app/spot/"
	}
	if s.SOTA.TokenURL == "" {
		s.SOTA.TokenURL = "https://sso.sota.org.uk/auth/realms/SOTA/protocol/openid-connect/token"
	}
	if s.SOTA.ClientID == "" {
		s.SOTA.ClientID = "sotawatch"
	}
	if s.SOTA.SpotURL == "" {
		s.SOTA.SpotURL = "https://api2.sota.org.uk/api/spots"
	}
	s.Callsign = strings.ToUpper(strings.TrimSpace(s.Callsign))
}

// Public is a copy safe to show on the settings page: passwords are left out.
func (s Settings) Public() Settings {
	s.IMAP.HasPass, s.IMAP.Password = s.IMAP.Password != "", ""
	s.SMTP.HasPass, s.SMTP.Password = s.SMTP.Password != "", ""
	s.SOTA.HasPass, s.SOTA.Password = s.SOTA.Password != "", ""
	return s
}

// Store loads and saves the settings file.
type Store struct {
	path string
	mu   sync.RWMutex
	s    Settings
}

// OpenStore reads dir/settings.json, or starts with defaults if there is none.
func OpenStore(dir string) (*Store, error) {
	st := &Store{path: filepath.Join(dir, "settings.json")}
	b, err := os.ReadFile(st.path)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		st.s.TestMode = true
	case err != nil:
		return nil, err
	default:
		if err := json.Unmarshal(b, &st.s); err != nil {
			return nil, err
		}
	}
	st.s.Defaults()
	return st, nil
}

// Get returns the current settings, passwords included.
func (st *Store) Get() Settings {
	st.mu.RLock()
	defer st.mu.RUnlock()
	return st.s
}

// Update saves new settings. A password left empty keeps the saved one.
func (st *Store) Update(n Settings) error {
	st.mu.Lock()
	defer st.mu.Unlock()
	keep := func(newPass *string, old string) {
		if *newPass == "" {
			*newPass = old
		}
	}
	keep(&n.IMAP.Password, st.s.IMAP.Password)
	keep(&n.SMTP.Password, st.s.SMTP.Password)
	keep(&n.SOTA.Password, st.s.SOTA.Password)
	n.IMAP.HasPass, n.SMTP.HasPass, n.SOTA.HasPass = false, false, false
	n.Defaults()
	b, err := json.MarshalIndent(n, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(st.path), 0o700); err != nil {
		return err
	}
	tmp := st.path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return err
	}
	if err := os.Rename(tmp, st.path); err != nil {
		return err
	}
	st.s = n
	return nil
}
