package gateway

import (
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"
)

// MapShare is the fallback for messages sent without a location: the newest
// tracking point from the user's Garmin MapShare page.
type MapShare struct {
	Name     string `json:"name"` // the part after share.garmin.com/
	Password string `json:"password,omitempty"`
	HasPass  bool   `json:"has_password"`
	MaxAge   int    `json:"max_age_min"` // ignore points older than this
}

var (
	placemarkRe = regexp.MustCompile(`(?s)<Placemark>.*?</Placemark>`)
	whenRe      = regexp.MustCompile(`<when>([^<]+)</when>`)
	coordsRe    = regexp.MustCompile(`<coordinates>\s*(-?[\d.]+),(-?[\d.]+)`)
)

// mapSharePos returns the newest MapShare point, if there is a recent one.
func (g *Gateway) mapSharePos(ms MapShare) (lat, lon float64, at time.Time, err error) {
	age := time.Duration(max(ms.MaxAge, 5)) * time.Minute
	since := time.Now().UTC().Add(-age).Format("2006-01-02T15:04Z")
	u := "https://share.garmin.com/Feed/Share/" + url.PathEscape(ms.Name) + "?d1=" + since
	req, _ := http.NewRequest("GET", u, nil)
	req.Header.Set("User-Agent", userAgent)
	if ms.Password != "" {
		req.SetBasicAuth("", ms.Password)
	}
	resp, err := g.client.Do(req)
	if err != nil {
		return 0, 0, at, err
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if resp.StatusCode != 200 {
		g.log.Error("MapShare: refused ("+resp.Status+")", "GET %s\n%s", u, clip(string(b), 2000))
		return 0, 0, at, fmt.Errorf("%s", resp.Status)
	}
	found := false
	for _, pm := range placemarkRe.FindAllString(string(b), -1) {
		w, c := whenRe.FindStringSubmatch(pm), coordsRe.FindStringSubmatch(pm)
		if w == nil || c == nil {
			continue
		}
		t, err := time.Parse(time.RFC3339, strings.TrimSpace(w[1]))
		if err != nil || t.Before(at) {
			continue
		}
		la, lo, ok := toLatLon(c[2], c[1])
		if !ok {
			continue
		}
		lat, lon, at, found = la, lo, t, true
	}
	if !found {
		g.log.Warn("MapShare: no point in the last "+age.String(), "GET %s\n%s", u, clip(string(b), 2000))
		return 0, 0, at, fmt.Errorf("no recent point")
	}
	g.log.Info("MapShare position", "%s at %s", posText(lat, lon), at.Format(time.RFC3339))
	return lat, lon, at, nil
}
