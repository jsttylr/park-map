package gateway

import (
	"bytes"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/smtp"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"
)

const userAgent = "park-map (+https://github.com/jsttylr/park-map)"

// do sends a request and logs the whole exchange, so a refusal from POTA,
// SOTA or Garmin can be read in the log viewer.
func (g *Gateway) do(event string, req *http.Request, reqBody string) (int, string, error) {
	req.Header.Set("User-Agent", userAgent)
	resp, err := g.client.Do(req)
	if err != nil {
		g.log.Error(event+": request failed", "%s %s\n%v", req.Method, req.URL, err)
		return 0, "", err
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
	detail := fmt.Sprintf("%s %s\nsent: %s\nanswer: %s\n%s", req.Method, req.URL, clip(reqBody, 2000), resp.Status, clip(string(b), 4000))
	if resp.StatusCode >= 300 {
		g.log.Error(event+": refused ("+resp.Status+")", detail)
		return resp.StatusCode, string(b), fmt.Errorf("%s", resp.Status)
	}
	g.log.Info(event+": ok", detail)
	return resp.StatusCode, string(b), nil
}

// spotPOTA posts one POTA spot. POTA's spot service doesn't need a login.
func (g *Gateway) spotPOTA(s Settings, ref string, c Command) error {
	body, _ := json.Marshal(map[string]string{
		"activator": s.Callsign, "spotter": s.Callsign, "frequency": fmtKHz(c.KHz),
		"reference": ref, "mode": c.Mode, "source": "park-map", "comments": c.Comment,
	})
	req, _ := http.NewRequest("POST", s.POTA.SpotURL, bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	_, _, err := g.do("POTA spot "+ref, req, string(body))
	return err
}

// sotaToken logs in to SOTA's single sign-on and keeps the tokens until
// shortly before they expire.
type sotaToken struct {
	mu           sync.Mutex
	user, access string
	id           string
	expires      time.Time
}

func (g *Gateway) sotaLogin(s Settings) (string, string, error) {
	t := &g.sota
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.user == s.SOTA.User && t.access != "" && time.Now().Before(t.expires) {
		return t.access, t.id, nil
	}
	form := url.Values{"grant_type": {"password"}, "client_id": {s.SOTA.ClientID}, "username": {s.SOTA.User},
		"password": {s.SOTA.Password}, "scope": {"openid"}}
	req, _ := http.NewRequest("POST", s.SOTA.TokenURL, strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	shown := url.Values{}
	for k, v := range form {
		shown[k] = v
	}
	shown.Set("password", "(hidden)")
	_, body, err := g.do("SOTA login", req, shown.Encode())
	if err != nil {
		return "", "", err
	}
	var r struct {
		Access  string `json:"access_token"`
		ID      string `json:"id_token"`
		Expires int    `json:"expires_in"`
	}
	if err := json.Unmarshal([]byte(body), &r); err != nil || r.Access == "" {
		g.log.Error("SOTA login: no token in the answer", clip(body, 2000))
		return "", "", fmt.Errorf("no token")
	}
	t.user, t.access, t.id = s.SOTA.User, r.Access, r.ID
	t.expires = time.Now().Add(time.Duration(max(r.Expires-30, 30)) * time.Second)
	return t.access, t.id, nil
}

// spotSOTA posts one SOTAwatch spot. ref is like W4V/BR-001.
func (g *Gateway) spotSOTA(s Settings, ref string, c Command) error {
	assoc, summit, ok := strings.Cut(ref, "/")
	if !ok {
		return fmt.Errorf("bad summit reference %s", ref)
	}
	access, id, err := g.sotaLogin(s)
	if err != nil {
		return fmt.Errorf("SOTA login failed")
	}
	body, _ := json.Marshal(map[string]string{
		"associationCode": assoc, "summitCode": summit, "activatorCallsign": s.Callsign, "callsign": s.Callsign,
		"frequency": fmtMHz(c.KHz), "mode": strings.ToLower(c.Mode), "comments": c.Comment, "type": "NORMAL",
	})
	req, _ := http.NewRequest("POST", s.SOTA.SpotURL, bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "bearer "+access)
	req.Header.Set("id_token", id)
	_, _, err = g.do("SOTA spot "+ref, req, string(body))
	if err != nil {
		g.sota.mu.Lock()
		g.sota.access = "" // log in again next time
		g.sota.mu.Unlock()
	}
	return err
}

var (
	inputRe  = regexp.MustCompile(`(?is)<input[^>]*>`)
	attrRe   = regexp.MustCompile(`(?is)\b(name|value|type)\s*=\s*("[^"]*"|'[^']*'|[^\s>]+)`)
	actionRe = regexp.MustCompile(`(?is)<form[^>]*\baction\s*=\s*("[^"]*"|'[^']*')`)
)

// garminDefaultPost is where Garmin's reply page sends replies.
const garminDefaultPost = "https://explore.garmin.com/TextMessage/TxtMsg"

// replyGarmin answers an inReach message the way the "reply" page Garmin
// links in every message does: open the page, then send its form with the
// reply text filled in. Garmin has no public way to do this, so every step is
// logged in full.
func (g *Gateway) replyGarmin(link, text string) error {
	req, _ := http.NewRequest("GET", link, nil)
	req.Header.Set("User-Agent", userAgent)
	resp, err := g.client.Do(req)
	if err != nil {
		g.log.Error("Garmin reply page: request failed", "%s\n%v", link, err)
		return err
	}
	page, _ := io.ReadAll(io.LimitReader(resp.Body, 512<<10))
	resp.Body.Close()
	final := resp.Request.URL
	form := url.Values{}
	for _, in := range inputRe.FindAllString(string(page), -1) {
		var name, value, typ string
		for _, a := range attrRe.FindAllStringSubmatch(in, -1) {
			v := strings.Trim(a[2], `"'`)
			switch strings.ToLower(a[1]) {
			case "name":
				name = v
			case "value":
				value = v
			case "type":
				typ = strings.ToLower(v)
			}
		}
		if name != "" && (typ == "hidden" || typ == "") {
			form.Set(name, htmlUnescape(value))
		}
	}
	q := final.Query()
	setIfEmpty := func(k, v string) {
		if form.Get(k) == "" && v != "" {
			form.Set(k, v)
		}
	}
	setIfEmpty("ReplyAddress", q.Get("adr"))
	setIfEmpty("Guid", q.Get("extId"))
	form.Set("ReplyMessage", text)
	post := garminDefaultPost
	if m := actionRe.FindStringSubmatch(string(page)); m != nil {
		if u, err := final.Parse(htmlUnescape(strings.Trim(m[1], `"'`))); err == nil && u.Host != "" {
			post = u.String()
		}
	}
	g.log.Info("Garmin reply page", "opened %s\nlanded on %s (%s)\nform fields found: %s", link, final, resp.Status, strings.Join(keys(form), ", "))
	req, _ = http.NewRequest("POST", post, strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Referer", final.String())
	_, _, err = g.do("Garmin reply", req, form.Encode())
	return err
}

func keys(v url.Values) []string {
	var out []string
	for k := range v {
		out = append(out, k)
	}
	return out
}

func htmlUnescape(s string) string {
	return strings.NewReplacer("&amp;", "&", "&quot;", `"`, "&#39;", "'", "&lt;", "<", "&gt;", ">").Replace(s)
}

// replyEmail answers an ordinary email through the SMTP server.
func (g *Gateway) replyEmail(s Settings, m *Message, text string) error {
	if s.SMTP.Host == "" {
		g.log.Warn("Email reply skipped", "no outgoing mail server is set, so this reply to %s was not sent: %s", m.From, text)
		return nil
	}
	from := s.SMTP.User
	subject := m.Subject
	if !strings.HasPrefix(strings.ToLower(subject), "re:") {
		subject = "Re: " + subject
	}
	msg := "From: " + from + "\r\nTo: " + m.From + "\r\nSubject: " + subject + "\r\n"
	if m.ID != "" {
		msg += "In-Reply-To: " + m.ID + "\r\nReferences: " + m.ID + "\r\n"
	}
	msg += "Content-Type: text/plain; charset=utf-8\r\n\r\n" + text + "\r\n"
	addr := net.JoinHostPort(s.SMTP.Host, strconv.Itoa(s.SMTP.Port))
	err := sendMail(s.SMTP, addr, from, m.From, []byte(msg))
	if err != nil {
		g.log.Error("Email reply failed", "to %s via %s\n%v", m.From, addr, err)
		return err
	}
	g.log.Info("Email reply sent", "to %s: %s", m.From, text)
	return nil
}

func sendMail(ms MailServer, addr, from, to string, msg []byte) error {
	tlsConf := &tls.Config{ServerName: ms.Host}
	var c *smtp.Client
	if ms.Security == "tls" {
		conn, err := tls.DialWithDialer(&net.Dialer{Timeout: 20 * time.Second}, "tcp", addr, tlsConf)
		if err != nil {
			return err
		}
		if c, err = smtp.NewClient(conn, ms.Host); err != nil {
			return err
		}
	} else {
		conn, err := net.DialTimeout("tcp", addr, 20*time.Second)
		if err != nil {
			return err
		}
		if c, err = smtp.NewClient(conn, ms.Host); err != nil {
			return err
		}
		if ms.Security == "starttls" {
			if err := c.StartTLS(tlsConf); err != nil {
				return err
			}
		}
	}
	defer c.Close()
	if ms.User != "" {
		if err := c.Auth(smtp.PlainAuth("", ms.User, ms.Password, ms.Host)); err != nil {
			return err
		}
	}
	if err := c.Mail(from); err != nil {
		return err
	}
	if err := c.Rcpt(to); err != nil {
		return err
	}
	w, err := c.Data()
	if err != nil {
		return err
	}
	if _, err := w.Write(msg); err != nil {
		return err
	}
	if err := w.Close(); err != nil {
		return err
	}
	return c.Quit()
}
