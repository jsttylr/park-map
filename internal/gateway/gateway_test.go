package gateway

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/jsttylr/park-map/internal/places"
	"github.com/paulmach/orb"
)

func box(x0, y0, x1, y1 float64) *places.Shape {
	return places.NewShape(orb.MultiPolygon{{{{x0, y0}, {x1, y0}, {x1, y1}, {x0, y1}, {x0, y0}}}})
}

func testGateway(t *testing.T) *Gateway {
	set := &places.Set{}
	set.Add(&places.Place{Ref: "US-0001", Name: "Big Forest", Shape: box(-80, 37, -79, 38)})
	set.Add(&places.Place{Ref: "US-0002", Name: "Wilderness", Shape: box(-79.6, 37.4, -79.4, 37.6)})
	set.Add(&places.Place{Kind: places.Summit, Ref: "W4V/BR-001", Name: "Peak", Shape: box(-79.51, 37.49, -79.49, 37.51)})
	set.ComputeOverlaps()
	st, err := OpenStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return New(set, st, NewLog(""))
}

const inreachMail = "From: Justin Taylor <justin@inreach.garmin.com>\r\n" +
	"To: parkmap@example.com\r\n" +
	"Subject: inReach message from Justin Taylor\r\n" +
	"Message-Id: <abc@garmin.com>\r\n" +
	"Content-Type: text/plain; charset=utf-8\r\n\r\n" +
	"spot 14.062 cw 'qrp\r\n\r\n" +
	"View the location or send a reply to Justin Taylor:\r\n" +
	"https://inreachlink.com/AbCdEf1\r\n\r\n" +
	"Justin Taylor sent this message from: Lat 37.500000 Lon -79.500000\r\n\r\n" +
	"Do not reply directly to this message.\r\n"

func TestParseMail(t *testing.T) {
	m, err := ParseMail([]byte(inreachMail))
	if err != nil {
		t.Fatal(err)
	}
	if m.From != "justin@inreach.garmin.com" || m.Text != "spot 14.062 cw 'qrp" || !m.HasPos ||
		m.Lat != 37.5 || m.Lon != -79.5 || m.ReplyURL != "https://inreachlink.com/AbCdEf1" {
		t.Errorf("%+v", m)
	}
	plain := "From: me@example.com\r\nTo: a+1234@example.com\r\nSubject: ?\r\n\r\n37.5, -79.5\r\n"
	m, _ = ParseMail([]byte(plain))
	if !m.HasPos || m.ReplyURL != "" || m.Text != "37.5, -79.5" {
		t.Errorf("plain: %+v", m)
	}
}

func TestParseCommand(t *testing.T) {
	cases := []struct {
		in   string
		want Command
	}{
		{"", Command{Kind: "lookup"}},
		{"?", Command{Kind: "lookup"}},
		{"spot 14062 cw 'qrp now", Command{Kind: "spot", KHz: 14062, Mode: "CW", Comment: "qrp now"}},
		{"s 14285", Command{Kind: "spot", KHz: 14285, Mode: "SSB"}},
		{"1234 s 7030", Command{Kind: "spot", KHz: 7030, Mode: "CW"}},
		{"pota 14074 ft8", Command{Kind: "pota", KHz: 14074, Mode: "FT8"}},
		{"sota 5357", Command{Kind: "sota", KHz: 5357, Mode: "SSB"}},
		{"pota us-1234,US-5678 146520", Command{Kind: "pota", Refs: []string{"US-1234", "US-5678"}, KHz: 146520, Mode: "FM"}},
		{"sota W4V/BR-001 7032 ssb", Command{Kind: "sota", Refs: []string{"W4V/BR-001"}, KHz: 7032, Mode: "SSB"}},
	}
	for _, c := range cases {
		got := ParseCommand(c.in, "1234")
		if !equal(got, c.want) {
			t.Errorf("%q: got %+v, want %+v", c.in, got, c.want)
		}
	}
	for _, bad := range []string{"spot banana", "spot 14.062", "spot 14602", "spot 1406", "spot 70320", "pota US1234 14062", "sota W4V/BR-01 7032", "pota"} {
		if c := ParseCommand(bad, ""); c.Err == "" {
			t.Errorf("%q accepted: %+v", bad, c)
		}
	}
	if c := ParseCommand("spot 14.062", ""); c.Err != "14.062 looks like MHz. Use kHz, e.g. 14062" {
		t.Errorf("MHz message %q", c.Err)
	}
	if c := ParseCommand("hello", ""); c.Kind != "help" {
		t.Error("unknown word should get help")
	}
}

func equal(a, b Command) bool {
	ja, _ := json.Marshal(a)
	jb, _ := json.Marshal(b)
	return string(ja) == string(jb)
}

func TestAllowed(t *testing.T) {
	s := Settings{AllowedSenders: []string{"justin@inreach.garmin.com", "@example.org"}, PIN: "1234"}
	for _, c := range []struct {
		m  Message
		ok bool
	}{
		{Message{From: "justin@inreach.garmin.com", Text: "1234 ?"}, true},
		{Message{From: "justin@inreach.garmin.com", To: "me+1234@gmail.com"}, true},
		{Message{From: "justin@inreach.garmin.com", Text: "?"}, false},
		{Message{From: "x@example.org", Text: "? 1234"}, true},
		{Message{From: "x@evil.com", Text: "1234"}, false},
	} {
		if ok, why := s.Allowed(&c.m); ok != c.ok {
			t.Errorf("%+v: %v %s", c.m, ok, why)
		}
	}
	s.PIN = ""
	s.AllowedSenders = []string{"no.reply.inreach@garmin.com"}
	if ok, _ := s.Allowed(&Message{From: "no.reply.inreach@garmin.com"}); ok {
		t.Error("Garmin's shared address accepted without a PIN")
	}
}

func TestAnswer(t *testing.T) {
	g := testGateway(t)
	s := g.store.Get()
	s.Callsign = "W1AW"
	s.POTA.Enabled = true
	s.TestMode = true
	m := &Message{Text: "?", Lat: 37.5, Lon: -79.5, HasPos: true}
	if got := g.Answer(s, m); !strings.HasPrefix(got, "2-fer: US-0001 Big Forest, US-0002 Wilderness. Summit W4V/BR-001") {
		t.Errorf("lookup: %q", got)
	}
	for _, c := range []struct{ in, want string }{
		{"spot 14062", "Spotted US-0001 US-0002 14062 kHz CW. SOTA off: W4V/BR-001 not spotted."},
		{"pota 14062", "Spotted US-0001 US-0002 14062 kHz CW."},
		{"pota US-9999 7200", "Spotted US-9999 7200 kHz SSB."},
		{"sota 7032", "SOTA spotting is off, or has no SOTAwatch login, on the settings page. Nothing spotted."},
	} {
		m.Text = c.in
		if got := g.Answer(s, m); got != c.want {
			t.Errorf("%s: %q", c.in, got)
		}
	}
	s.SOTA = SOTASettings{Enabled: true, User: "w1aw"}
	for _, c := range []struct{ in, want string }{
		{"sota 7032", "Spotted W4V/BR-001 7032 kHz CW."},
		{"spot 7032", "Spotted US-0001 US-0002 W4V/BR-001 7032 kHz CW."},
	} {
		m.Text = c.in
		if got := g.Answer(s, m); got != c.want {
			t.Errorf("%s: %q", c.in, got)
		}
	}
	m.Lat, m.Lon = 30, -90
	m.Text = "sota 7032"
	if got := g.Answer(s, m); got != "No summit at 30.00000,-90.00000. Nothing spotted." {
		t.Errorf("no summit: %q", got)
	}
}

// TestSpots runs POTA, SOTA and Garmin against stand-in servers.
func TestSpots(t *testing.T) {
	var got []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		got = append(got, r.Method+" "+r.URL.Path+" "+string(b))
		switch r.URL.Path {
		case "/token":
			json.NewEncoder(w).Encode(map[string]any{"access_token": "AT", "id_token": "IT", "expires_in": 300})
		case "/sota":
			if r.Header.Get("Authorization") != "bearer AT" || r.Header.Get("id_token") != "IT" {
				http.Error(w, "no auth", 401)
			}
		case "/link":
			http.Redirect(w, r, "/r?extId=GUID1", http.StatusFound)
		case "/r":
			http.Redirect(w, r, "/web/reply/TINY1", http.StatusFound)
		case "/web/reply/TINY1":
			if r.Method == "GET" {
				io.WriteString(w, `<script src="/web/_next/static/chunks/app/(reply)/reply/%5BtinyUrlId%5D/page-abc123.js" async></script>`)
			} else if r.Header.Get("Next-Action") != "abcdef0123456789abcdef" {
				http.Error(w, "wrong action", 404)
			} else {
				io.WriteString(w, `0:{"a":"$@1"}`)
			}
		case "/web/_next/static/chunks/app/(reply)/reply/[tinyUrlId]/page-abc123.js":
			io.WriteString(w, `let F=(0,R.createServerReference)("abcdef0123456789abcdef",R.callServer,void 0,R.findSourceMapURL,"sendReplyAction");`)
		}
	}))
	defer srv.Close()
	g := testGateway(t)
	s := g.store.Get()
	s.Callsign, s.POTA.Enabled, s.POTA.SpotURL, s.TestMode = "W1AW", true, srv.URL+"/pota", false
	s.SOTA = SOTASettings{Enabled: true, User: "w1aw", Password: "pw", TokenURL: srv.URL + "/token", ClientID: "sotawatch", SpotURL: srv.URL + "/sota"}
	m := &Message{Text: "spot 7032 cw hi", Lat: 37.5, Lon: -79.5, HasPos: true}
	if r := g.Answer(s, m); r != "Spotted US-0001 US-0002 W4V/BR-001 7032 kHz CW." {
		t.Errorf("reply %q\n%v", r, g.log.Since(0))
	}
	if err := g.replyGarmin(srv.URL+"/link", "hello there"); err != nil {
		t.Fatal(err)
	}
	last := got[len(got)-1]
	if last != `POST /web/reply/TINY1 ["TINY1","hello there"]` {
		t.Errorf("garmin post %q\n%v", last, g.log.Since(0))
	}
	if !strings.Contains(got[0], `"reference":"US-0001"`) || !strings.Contains(got[0], `"frequency":"7032"`) ||
		!strings.Contains(got[0], `"comments":"2fer US-0002 hi"`) || strings.Contains(strings.Join(got, "\n"), `"reference":"US-0002"`) {
		t.Errorf("pota %q", got[0])
	}
	if !strings.Contains(strings.Join(got, "\n"), `"summitCode":"BR-001"`) {
		t.Errorf("sota %v", got)
	}
	got = nil
	g.Answer(s, &Message{Text: "sota W4V/BR-001 7032 test one", HasPos: false})
	if len(got) == 0 || !strings.Contains(got[len(got)-1], `"type":"TEST"`) {
		t.Errorf("sota test spot %v", got)
	}
}

func TestStoreKeepsPasswords(t *testing.T) {
	dir := t.TempDir()
	st, _ := OpenStore(dir)
	s := st.Get()
	s.IMAP.Password = "secret"
	if err := st.Update(s); err != nil {
		t.Fatal(err)
	}
	pub := st.Get().Public()
	if pub.IMAP.Password != "" || !pub.IMAP.HasPass {
		t.Errorf("public %+v", pub.IMAP)
	}
	if err := st.Update(pub); err != nil || st.Get().IMAP.Password != "secret" {
		t.Errorf("password lost: %v", err)
	}
	st2, _ := OpenStore(dir)
	if st2.Get().IMAP.Password != "secret" {
		t.Error("not saved")
	}
}

func TestMapShareKML(t *testing.T) {
	now := time.Now().UTC()
	kml := `<kml><Document><Folder><Placemark><TimeStamp><when>` + now.Add(-20*time.Minute).Format(time.RFC3339) + `</when></TimeStamp><Point><coordinates>-79.1,37.1,100</coordinates></Point></Placemark>` +
		`<Placemark><TimeStamp><when>` + now.Add(-5*time.Minute).Format(time.RFC3339) + `</when></TimeStamp><Point><coordinates>-79.5,37.5,100</coordinates></Point></Placemark></Folder></Document></kml>`
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if _, p, _ := r.BasicAuth(); p != "pw" {
			http.Error(w, "no", 401)
			return
		}
		io.WriteString(w, kml)
	}))
	defer srv.Close()
	g := testGateway(t)
	g.client.Transport = rewrite{srv.URL}
	lat, lon, _, err := g.mapSharePos(MapShare{Name: "Me", Password: "pw", MaxAge: 60})
	if err != nil || lat != 37.5 || lon != -79.5 {
		t.Errorf("%v %v %v %v", lat, lon, err, g.log.Since(0))
	}
}

// rewrite sends every request to a test server.
type rewrite struct{ to string }

func (r rewrite) RoundTrip(req *http.Request) (*http.Response, error) {
	u, _ := url.Parse(r.to)
	req.URL.Scheme, req.URL.Host = u.Scheme, u.Host
	return http.DefaultTransport.RoundTrip(req)
}
