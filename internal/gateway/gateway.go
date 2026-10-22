package gateway

import (
	"context"
	"crypto/tls"
	"fmt"
	"io"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/emersion/go-imap"
	"github.com/emersion/go-imap/client"
	"github.com/jsttylr/park-map/internal/places"
)

// Gateway checks the mailbox and answers messages.
type Gateway struct {
	set    *places.Set
	store  *Store
	log    *Log
	client *http.Client
	sota   sotaToken
	wake   chan struct{}
}

func New(set *places.Set, store *Store, log *Log) *Gateway {
	return &Gateway{set: set, store: store, log: log, client: &http.Client{Timeout: 30 * time.Second}, wake: make(chan struct{}, 1)}
}

func (g *Gateway) Log() *Log     { return g.log }
func (g *Gateway) Store() *Store { return g.store }

// CheckNow asks the loop to check the mailbox right away.
func (g *Gateway) CheckNow() {
	select {
	case g.wake <- struct{}{}:
	default:
	}
}

// Run checks the mailbox on the set interval until ctx ends.
func (g *Gateway) Run(ctx context.Context) {
	for {
		s := g.store.Get()
		if s.Enabled && s.IMAP.Host != "" {
			if n, err := g.Poll(s); err != nil {
				g.log.Error("Mailbox check failed", err.Error())
			} else if n > 0 {
				g.log.Info("Mailbox checked", "%d new message(s)", n)
			}
		}
		select {
		case <-ctx.Done():
			return
		case <-g.wake:
		case <-time.After(time.Duration(s.IntervalSec) * time.Second):
		}
	}
}

func dialIMAP(ms MailServer) (*client.Client, error) {
	addr := net.JoinHostPort(ms.Host, strconv.Itoa(ms.Port))
	tlsConf := &tls.Config{ServerName: ms.Host}
	dialer := &net.Dialer{Timeout: 20 * time.Second}
	var c *client.Client
	var err error
	if ms.Security == "tls" {
		c, err = client.DialWithDialerTLS(dialer, addr, tlsConf)
	} else {
		c, err = client.DialWithDialer(dialer, addr)
		if err == nil && ms.Security == "starttls" {
			err = c.StartTLS(tlsConf)
		}
	}
	if err != nil {
		return nil, fmt.Errorf("connecting to %s: %w", addr, err)
	}
	c.Timeout = time.Minute
	if err := c.Login(ms.User, ms.Password); err != nil {
		c.Logout()
		return nil, fmt.Errorf("logging in to %s as %s: %w", addr, ms.User, err)
	}
	return c, nil
}

// TestMailbox logs in and counts unread messages without touching them.
func (g *Gateway) TestMailbox(s Settings) (string, error) {
	c, err := dialIMAP(s.IMAP)
	if err != nil {
		return "", err
	}
	defer c.Logout()
	mbox, err := c.Select(s.Folder, true)
	if err != nil {
		return "", fmt.Errorf("opening folder %s: %w", s.Folder, err)
	}
	return fmt.Sprintf("Logged in. %s has %d messages, %d unread.", s.Folder, mbox.Messages, mbox.Unseen), nil
}

// Poll handles every unread message in the folder and marks it read.
func (g *Gateway) Poll(s Settings) (int, error) {
	c, err := dialIMAP(s.IMAP)
	if err != nil {
		return 0, err
	}
	defer c.Logout()
	if _, err := c.Select(s.Folder, false); err != nil {
		return 0, fmt.Errorf("opening folder %s: %w", s.Folder, err)
	}
	crit := imap.NewSearchCriteria()
	crit.WithoutFlags = []string{imap.SeenFlag}
	uids, err := c.UidSearch(crit)
	if err != nil || len(uids) == 0 {
		return 0, err
	}
	if len(uids) > 20 {
		uids = uids[len(uids)-20:]
	}
	seq := new(imap.SeqSet)
	seq.AddNum(uids...)
	section := &imap.BodySectionName{Peek: true}
	ch := make(chan *imap.Message, len(uids))
	done := make(chan error, 1)
	go func() { done <- c.UidFetch(seq, []imap.FetchItem{imap.FetchUid, section.FetchItem()}, ch) }()
	type raw struct {
		uid  uint32
		body []byte
	}
	var msgs []raw
	for m := range ch {
		if r := m.GetBody(section); r != nil {
			b, _ := io.ReadAll(io.LimitReader(r, 4<<20))
			msgs = append(msgs, raw{m.Uid, b})
		}
	}
	if err := <-done; err != nil {
		return 0, fmt.Errorf("reading messages: %w", err)
	}
	for _, r := range msgs {
		// Mark it read first, so a message that keeps failing isn't answered
		// (and charged for) again on every check.
		one := new(imap.SeqSet)
		one.AddNum(r.uid)
		if err := c.UidStore(one, imap.FormatFlagsOp(imap.AddFlags, true), []interface{}{imap.SeenFlag}, nil); err != nil {
			g.log.Error("Couldn't mark a message read, skipping it", err.Error())
			continue
		}
		m, err := ParseMail(r.body)
		if err != nil {
			g.log.Error("Couldn't read a message", err.Error())
			continue
		}
		g.Handle(s, m)
	}
	return len(msgs), nil
}

// Handle answers one message and returns the reply text.
func (g *Gateway) Handle(s Settings, m *Message) string {
	pos := "no position"
	if m.HasPos {
		pos = posText(m.Lat, m.Lon)
	}
	g.log.Info("Message from "+m.From, "subject: %s\ntext: %q\nposition: %s\nreply link: %s\n\nfull body:\n%s", m.Subject, m.Text, pos, m.ReplyURL, clip(m.Body, 4000))
	if ok, why := s.Allowed(m); !ok {
		g.log.Warn("Message ignored", why)
		return ""
	}
	reply := g.Answer(s, m)
	if reply == "" {
		return ""
	}
	if s.TestMode {
		g.log.Info("Test mode: reply not sent", reply)
		return reply
	}
	if m.ReplyURL != "" {
		g.replyGarmin(m.ReplyURL, reply)
	} else {
		g.replyEmail(s, m, reply)
	}
	return reply
}

// Answer carries out the message's command and returns the reply text.
func (g *Gateway) Answer(s Settings, m *Message) string {
	c := ParseCommand(m.Text, s.PIN)
	if c.Err != "" && c.Kind != "help" {
		return clip(c.Err, 160)
	}
	switch c.Kind {
	case "help":
		return clip(strings.TrimSpace(c.Err+" "+helpText), 160)
	case "lookup":
		if !m.HasPos {
			return "No position in the message. Turn on location sharing or type lat,lon."
		}
		_, _, in, near := here(g.set, m.Lat, m.Lon)
		return places.Summary(in, near, 160)
	}
	if s.Callsign == "" {
		return "Set your callsign on the settings page first."
	}
	var potaRefs, sotaRefs []string
	switch c.Kind {
	case "spot":
		if !m.HasPos {
			return "No position in the message, so no spot. Use pota/sota with a reference."
		}
		potaRefs, sotaRefs, _, _ = here(g.set, m.Lat, m.Lon)
		if len(potaRefs)+len(sotaRefs) == 0 {
			return "No park or summit at " + posText(m.Lat, m.Lon) + ". Nothing spotted."
		}
	case "pota":
		potaRefs = c.Refs
	case "sota":
		sotaRefs = c.Refs
	}
	var ok, bad []string
	try := func(prog string, enabled bool, refs []string, post func(Settings, string, Command) error) {
		for _, ref := range refs {
			switch {
			case !enabled:
				bad = append(bad, ref+" ("+prog+" spotting off)")
			case s.TestMode:
				g.log.Info("Test mode: "+prog+" spot not sent", "%s %s kHz %s %q as %s", ref, fmtKHz(c.KHz), c.Mode, c.Comment, s.Callsign)
				ok = append(ok, ref)
			case post(s, ref, c) != nil:
				bad = append(bad, ref)
			default:
				ok = append(ok, ref)
			}
		}
	}
	try("POTA", s.POTA.Enabled, potaRefs, g.spotPOTA)
	try("SOTA", s.SOTA.Enabled && s.SOTA.User != "", sotaRefs, g.spotSOTA)
	reply := ""
	if len(ok) > 0 {
		reply = "Spotted " + strings.Join(ok, " ") + " " + fmtMHz(c.KHz) + " " + c.Mode + "."
	}
	if len(bad) > 0 {
		reply += " FAILED: " + strings.Join(bad, " ") + ". See log."
	}
	return clip(strings.TrimSpace(reply), 160)
}
