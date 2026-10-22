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

// garminActionID is the id Garmin's reply page used for its "send reply"
// action when this was written. It changes whenever Garmin updates the page,
// so the gateway reads the current one from the page's scripts first.
const garminActionID = "6083881b56e00900885da0354bcbdb97cd101354aa"

var (
	tinyPathRe  = regexp.MustCompile(`/reply/([A-Za-z0-9_-]+)`)
	tinyJSONRe  = regexp.MustCompile(`tinyUrlId\\?"\s*:\s*\\?"([A-Za-z0-9_-]+)`)
	pageChunkRe = regexp.MustCompile(`[^"'\s]*/_next/static/chunks/app/[^"'\s]*reply[^"'\s]*page-[0-9a-f]+\.js`)
	sendActRe   = regexp.MustCompile(`createServerReference\)\("([0-9a-f]{20,})"[^)]*"sendReplyAction"\)`)
)

// replyGarmin answers an inReach message the way the "reply" page Garmin
// links in every message does. The page (messenger.garmin.com) sends a reply
// by calling its sendReplyAction with the message's short id and the text, so
// the gateway opens the page, finds both, and makes the same call. Garmin has
// no public way to do this, so every step is logged in full.
func (g *Gateway) replyGarmin(link, text string) error {
	page, final, err := g.fetch(link)
	if err != nil {
		g.log.Error("Garmin reply page: request failed", "%s\n%v", link, err)
		return err
	}
	tiny := ""
	if m := tinyPathRe.FindStringSubmatch(final.Path); m != nil {
		tiny = m[1]
	} else if m := tinyJSONRe.FindStringSubmatch(page); m != nil {
		tiny = m[1]
	} else {
		tiny = final.Query().Get("extId")
	}
	if tiny == "" {
		g.log.Error("Garmin reply page: no message id", "opened %s\nlanded on %s\n%s", link, final, clip(page, 4000))
		return fmt.Errorf("no message id on Garmin's reply page")
	}
	action, from := garminActionID, "built in"
	for _, src := range pageChunkRe.FindAllString(page, -1) {
		u, err := final.Parse(src)
		if err != nil {
			continue
		}
		js, _, err := g.fetch(u.String())
		if err != nil {
			continue
		}
		if m := sendActRe.FindStringSubmatch(js); m != nil {
			action, from = m[1], u.String()
			break
		}
	}
	post := final.ResolveReference(&url.URL{Path: "/web/reply/" + tiny}).String()
	g.log.Info("Garmin reply page", "opened %s\nlanded on %s\nmessage id: %s\nsend action: %s (from %s)", link, final, tiny, action, from)
	if strings.HasPrefix(text, "$") {
		text = "$" + text // the page's encoding escapes a leading $ this way
	}
	body, _ := json.Marshal([]string{tiny, text})
	req, _ := http.NewRequest("POST", post, bytes.NewReader(body))
	req.Header.Set("Content-Type", "text/plain;charset=UTF-8")
	req.Header.Set("Accept", "text/x-component")
	req.Header.Set("Next-Action", action)
	req.Header.Set("Origin", final.Scheme+"://"+final.Host)
	req.Header.Set("Referer", post)
	_, answer, err := g.do("Garmin reply", req, string(body))
	if err == nil && strings.Contains(answer, `"digest"`) {
		err = fmt.Errorf("Garmin's page reported an error")
		g.log.Error("Garmin reply: page reported an error", "%s", clip(answer, 4000))
	}
	return err
}

// fetch GETs a page, following redirects, and returns it with the address
// it ended on.
func (g *Gateway) fetch(link string) (string, *url.URL, error) {
	req, err := http.NewRequest("GET", link, nil)
	if err != nil {
		return "", nil, err
	}
	req.Header.Set("User-Agent", userAgent)
	resp, err := g.client.Do(req)
	if err != nil {
		return "", nil, err
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(io.LimitReader(resp.Body, 2<<20))
	if resp.StatusCode >= 300 {
		return string(b), resp.Request.URL, fmt.Errorf("%s", resp.Status)
	}
	return string(b), resp.Request.URL, nil
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
