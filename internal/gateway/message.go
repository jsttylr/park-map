package gateway

import (
	"bytes"
	"html"
	"io"
	"regexp"
	"strconv"
	"strings"

	_ "github.com/emersion/go-message/charset" // non-UTF-8 mail
	"github.com/emersion/go-message/mail"
)

// Message is an incoming message with what the gateway needs from it.
type Message struct {
	From     string
	To       string
	Subject  string
	ID       string
	Body     string // the whole text body, for the log
	Text     string // what the sender typed
	Lat, Lon float64
	HasPos   bool
	ReplyURL string // Garmin's reply link; empty for ordinary email
}

// ParseMail reads a raw email.
func ParseMail(raw []byte) (*Message, error) {
	mr, err := mail.CreateReader(bytes.NewReader(raw))
	if err != nil {
		return nil, err
	}
	m := &Message{}
	if from, err := mr.Header.AddressList("From"); err == nil && len(from) > 0 {
		m.From = strings.ToLower(from[0].Address)
	}
	m.To = mr.Header.Get("To")
	m.Subject, _ = mr.Header.Subject()
	m.ID = mr.Header.Get("Message-Id")
	var plain, htmlBody string
	for {
		p, err := mr.NextPart()
		if err != nil {
			break
		}
		h, ok := p.Header.(*mail.InlineHeader)
		if !ok {
			continue
		}
		ct, _, _ := h.ContentType()
		b, _ := io.ReadAll(io.LimitReader(p.Body, 1<<20))
		switch {
		case ct == "text/plain" && plain == "":
			plain = string(b)
		case ct == "text/html" && htmlBody == "":
			htmlBody = string(b)
		}
	}
	if plain == "" && htmlBody != "" {
		plain = stripHTML(htmlBody)
	}
	m.Body = strings.ReplaceAll(plain, "\r\n", "\n")
	parseBody(m)
	return m, nil
}

var (
	tagRe     = regexp.MustCompile(`(?s)<(script|style).*?</(script|style)>|<br\s*/?>|</p>|</div>|<[^>]+>`)
	latLonRe  = regexp.MustCompile(`(?i)\blat(?:itude)?[:\s]*(-?\d{1,2}(?:\.\d+)?)[\s,;]*lon(?:g|gitude)?[:\s]*(-?\d{1,3}(?:\.\d+)?)`)
	pairRe    = regexp.MustCompile(`(-?\d{1,2}\.\d+)\s*[, ]\s*(-?\d{1,3}\.\d+)`)
	replyRe   = regexp.MustCompile(`(?i)https?://(?:[a-z0-9-]+\.)*(?:inreachlink\.com|garmin\.com)/[^\s"'<>]+`)
	garminRef = regexp.MustCompile(`(?i)inreachlink\.com|garmin\.com|sent this message from|view the location|do not reply directly`)
)

func stripHTML(s string) string {
	s = tagRe.ReplaceAllStringFunc(s, func(t string) string {
		if strings.HasPrefix(t, "<br") || strings.HasPrefix(t, "</") {
			return "\n"
		}
		// keep link targets, which hold Garmin's reply link
		if i := strings.Index(t, "href=\""); i >= 0 {
			if j := strings.Index(t[i+6:], "\""); j >= 0 {
				return " " + t[i+6:i+6+j] + " "
			}
		}
		return ""
	})
	return html.UnescapeString(s)
}

// parseBody finds the typed text, the position and Garmin's reply link.
func parseBody(m *Message) {
	body := m.Body
	if u := replyRe.FindString(body); u != "" {
		m.ReplyURL = html.UnescapeString(u)
	}
	// The typed text is the first non-empty line before Garmin's own lines.
	// Like SOTAmat, ";;" also ends it.
	for _, line := range strings.Split(body, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		if !garminRef.MatchString(line) {
			m.Text = line
		}
		break
	}
	if i := strings.Index(m.Text, ";;"); i >= 0 {
		m.Text = m.Text[:i]
	}
	m.Text = strings.TrimSpace(m.Text)
	if m.Text == "" && m.ReplyURL == "" {
		m.Text = strings.TrimSpace(m.Subject)
	}
	// Coordinates typed in a lookup win over the device's position.
	first, _, _ := strings.Cut(strings.ToLower(m.Text), " ")
	isSpot := map[string]bool{"spot": true, "s": true, "pota": true, "p": true, "sota": true}[first]
	if lat, lon, ok := findPair(m.Text); ok && !isSpot {
		m.Lat, m.Lon, m.HasPos = lat, lon, true
		return
	}
	if g := latLonRe.FindStringSubmatch(body); g != nil {
		if lat, lon, ok := toLatLon(g[1], g[2]); ok {
			m.Lat, m.Lon, m.HasPos = lat, lon, true
		}
	}
}

func findPair(s string) (float64, float64, bool) {
	if g := latLonRe.FindStringSubmatch(s); g != nil {
		return toLatLon(g[1], g[2])
	}
	if g := pairRe.FindStringSubmatch(s); g != nil {
		return toLatLon(g[1], g[2])
	}
	return 0, 0, false
}

func toLatLon(a, b string) (float64, float64, bool) {
	lat, err1 := strconv.ParseFloat(a, 64)
	lon, err2 := strconv.ParseFloat(b, 64)
	if err1 != nil || err2 != nil || lat < -90 || lat > 90 || lon < -180 || lon > 180 {
		return 0, 0, false
	}
	return lat, lon, true
}

// Allowed reports whether a message may use the gateway, and why not.
func (s Settings) Allowed(m *Message) (bool, string) {
	ok := false
	for _, a := range s.AllowedSenders {
		a = strings.ToLower(strings.TrimSpace(a))
		if a != "" && (m.From == a || strings.HasPrefix(a, "@") && strings.HasSuffix(m.From, a)) {
			ok = true
		}
	}
	if !ok {
		return false, "sender " + m.From + " is not in the allowed senders list"
	}
	if s.PIN != "" && !strings.Contains(m.To, s.PIN) && !hasWord(m.Text, s.PIN) {
		return false, "message has no PIN"
	}
	return true, ""
}

func hasWord(s, w string) bool {
	for _, f := range strings.Fields(s) {
		if f == w {
			return true
		}
	}
	return false
}
