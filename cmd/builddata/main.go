// Command builddata matches US POTA parks to PAD-US boundary polygons.
//
// Inputs are the POTA park list (all_parks_ext.csv) and PAD-US polygons as
// newline-delimited GeoJSON in WGS84, optionally gzipped (see fetchpadus). Output is a gzipped
// GeoJSON file with one feature per park (its boundary, or just its point when
// no boundary matched) and a CSV report for checking match quality.
package main

import (
	"bufio"
	"compress/gzip"
	"encoding/csv"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log"
	"math"
	"os"
	"sort"
	"strconv"
	"strings"

	"github.com/jsttylr/park-map/internal/match"
	"github.com/paulmach/orb"
	"github.com/paulmach/orb/geojson"
	"github.com/paulmach/orb/planar"
)

const (
	cell       = 0.05  // grid cell size for the park point index, degrees
	nearKm     = 2.0   // how far from a park point a polygon may sit and still be a candidate
	gatherKm   = 300.0 // how far away parcels of the same unit are still collected
	kmPerDeg   = 111.0
	minHigh    = 0.5
	minMedium  = 0.3
	minNearMed = 0.75
)

type park struct {
	Ref, Name, Loc string
	Pt             orb.Point
	best           *candidate
	geom           orb.MultiPolygon
}

type candidate struct {
	Key, Unit, Manager, Designation, Layer string
	Score, DistKm                          float64
	Contains                               bool
}

func (c *candidate) rank() float64 {
	r := c.Score - c.DistKm*0.05
	if c.Contains {
		r += 0.15
	}
	return r
}

func (c *candidate) confidence() string {
	switch {
	case c == nil:
		return "none"
	case c.Contains && c.Score >= minHigh:
		return "high"
	case c.Contains && c.Score >= minMedium, !c.Contains && c.Score >= minNearMed:
		return "medium"
	}
	return "none"
}

type gridKey struct{ x, y int }

type index map[gridKey][]*park

func cellOf(p orb.Point) gridKey {
	return gridKey{int(math.Floor(p[0] / cell)), int(math.Floor(p[1] / cell))}
}

func (ix index) within(b orb.Bound) []*park {
	lo, hi := cellOf(b.Min), cellOf(b.Max)
	var out []*park
	for x := lo.x; x <= hi.x; x++ {
		for y := lo.y; y <= hi.y; y++ {
			for _, p := range ix[gridKey{x, y}] {
				if b.Contains(p.Pt) {
					out = append(out, p)
				}
			}
		}
	}
	return out
}

func main() {
	potaPath := flag.String("pota", "all_parks_ext.csv", "POTA park list CSV")
	prefix := flag.String("prefix", "US-", "park reference prefix to include")
	out := flag.String("out", "us-parks.geojson.gz", "output GeoJSON (gzipped)")
	report := flag.String("report", "report.csv", "output match report CSV")
	flag.Parse()
	layers := flag.Args()
	if len(layers) == 0 {
		log.Fatal("usage: builddata [flags] padus.geojsonl.gz ...")
	}

	parks, err := readPOTA(*potaPath, *prefix)
	if err != nil {
		log.Fatal(err)
	}
	ix := index{}
	for _, p := range parks {
		k := cellOf(p.Pt)
		ix[k] = append(ix[k], p)
	}
	log.Printf("%d active %s parks", len(parks), *prefix)

	// Pass 1: score every polygon near each park point.
	for _, l := range layers {
		n := 0
		err := eachFeature(l, func(f *geojson.Feature, mp orb.MultiPolygon) {
			n++
			b := mp.Bound().Pad(nearKm / kmPerDeg)
			for _, p := range ix.within(b) {
				c := score(f, l, mp, p)
				if c != nil && (p.best == nil || c.rank() > p.best.rank()) {
					p.best = c
				}
			}
		})
		if err != nil {
			log.Fatal(err)
		}
		log.Printf("pass 1: %s, %d features", l, n)
	}

	// Pass 2: collect every parcel of each chosen unit.
	want := map[string][]*park{}
	for _, p := range parks {
		if p.best.confidence() != "none" {
			want[p.best.Key] = append(want[p.best.Key], p)
		}
	}
	for _, l := range layers {
		err := eachFeature(l, func(f *geojson.Feature, mp orb.MultiPolygon) {
			k := unitKey(f)
			if k == "" || len(want[k]) == 0 {
				return
			}
			b := mp.Bound()
			for _, p := range want[k] {
				if b.Pad(gatherKm / kmPerDeg).Contains(p.Pt) {
					p.geom = append(p.geom, mp...)
				}
			}
		})
		if err != nil {
			log.Fatal(err)
		}
		log.Printf("pass 2: %s", l)
	}

	if err := writeGeoJSON(*out, parks); err != nil {
		log.Fatal(err)
	}
	if err := writeReport(*report, parks); err != nil {
		log.Fatal(err)
	}
	summarize(parks)
}

func readPOTA(path, prefix string) ([]*park, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	r := csv.NewReader(f)
	head, err := r.Read()
	if err != nil {
		return nil, err
	}
	col := map[string]int{}
	for i, h := range head {
		col[h] = i
	}
	for _, need := range []string{"reference", "name", "active", "locationDesc", "latitude", "longitude"} {
		if _, ok := col[need]; !ok {
			return nil, fmt.Errorf("POTA CSV missing column %q", need)
		}
	}
	var parks []*park
	for {
		rec, err := r.Read()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, err
		}
		ref := rec[col["reference"]]
		if !strings.HasPrefix(ref, prefix) || rec[col["active"]] != "1" {
			continue
		}
		lat, err1 := strconv.ParseFloat(rec[col["latitude"]], 64)
		lon, err2 := strconv.ParseFloat(rec[col["longitude"]], 64)
		if err1 != nil || err2 != nil {
			continue
		}
		parks = append(parks, &park{Ref: ref, Name: rec[col["name"]], Loc: rec[col["locationDesc"]], Pt: orb.Point{lon, lat}})
	}
	return parks, nil
}

// eachFeature streams a newline-delimited GeoJSON file, passing polygonal features.
func eachFeature(path string, fn func(*geojson.Feature, orb.MultiPolygon)) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	var r io.Reader = f
	if strings.HasSuffix(path, ".gz") {
		gz, err := gzip.NewReader(f)
		if err != nil {
			return err
		}
		defer gz.Close()
		r = gz
	}
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 1<<20), 1<<30)
	for sc.Scan() {
		line := sc.Bytes()
		if len(line) > 0 && line[0] == 0x1e { // RFC 8142 record separator
			line = line[1:]
		}
		if len(line) == 0 {
			continue
		}
		feat, err := geojson.UnmarshalFeature(line)
		if err != nil || feat.Geometry == nil {
			continue
		}
		switch g := feat.Geometry.(type) {
		case orb.Polygon:
			fn(feat, orb.MultiPolygon{g})
		case orb.MultiPolygon:
			fn(feat, g)
		}
	}
	return sc.Err()
}

func prop(f *geojson.Feature, names ...string) string {
	for _, n := range names {
		if s, ok := f.Properties[n].(string); ok && s != "" {
			return s
		}
	}
	return ""
}

func unitKey(f *geojson.Feature) string {
	u := prop(f, "Unit_Nm")
	if u == "" {
		return ""
	}
	return match.Key(u) + "|" + prop(f, "Mang_Name") + "|" + prop(f, "FeatClass")
}

func score(f *geojson.Feature, layer string, mp orb.MultiPolygon, p *park) *candidate {
	unit := prop(f, "Unit_Nm")
	if unit == "" {
		return nil
	}
	s := 0.0
	if match.SharesName(p.Name, unit) {
		s = match.Similarity(p.Name, unit)
	}
	if loc := prop(f, "Loc_Nm"); loc != "" && match.SharesName(p.Name, loc) {
		s = math.Max(s, match.Similarity(p.Name, loc))
	}
	if s < minMedium {
		return nil
	}
	c := &candidate{
		Key: unitKey(f), Unit: unit, Layer: prop(f, "FeatClass"), Score: s,
		Manager:     prop(f, "Mang_Name"),
		Designation: prop(f, "Des_Tp"),
	}
	if planar.MultiPolygonContains(mp, p.Pt) {
		c.Contains = true
	} else {
		c.DistKm = planar.DistanceFrom(mp, p.Pt) * kmPerDeg
		if c.DistKm > nearKm {
			return nil
		}
	}
	return c
}

func round(mp orb.MultiPolygon) orb.MultiPolygon {
	r := func(v float64) float64 { return math.Round(v*1e5) / 1e5 }
	for _, poly := range mp {
		for _, ring := range poly {
			for i := range ring {
				ring[i] = orb.Point{r(ring[i][0]), r(ring[i][1])}
			}
		}
	}
	return mp
}

func writeGeoJSON(path string, parks []*park) error {
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()
	gz := gzip.NewWriter(f)
	w := bufio.NewWriter(gz)
	w.WriteString(`{"type":"FeatureCollection","features":[`)
	for i, p := range parks {
		var g orb.Geometry = p.Pt
		if len(p.geom) > 0 {
			g = round(p.geom)
		}
		ft := geojson.NewFeature(g)
		ft.Properties["ref"] = p.Ref
		ft.Properties["name"] = p.Name
		ft.Properties["loc"] = p.Loc
		ft.Properties["lat"] = p.Pt[1]
		ft.Properties["lon"] = p.Pt[0]
		ft.Properties["match"] = p.best.confidence()
		if p.best != nil && len(p.geom) > 0 {
			ft.Properties["boundary_name"] = p.best.Unit
			ft.Properties["manager"] = p.best.Manager
			ft.Properties["designation"] = p.best.Designation
		}
		b, err := json.Marshal(ft)
		if err != nil {
			return err
		}
		if i > 0 {
			w.WriteString(",\n")
		}
		w.Write(b)
	}
	w.WriteString("]}\n")
	if err := w.Flush(); err != nil {
		return err
	}
	return gz.Close()
}

func writeReport(path string, parks []*park) error {
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()
	w := csv.NewWriter(f)
	w.Write([]string{"ref", "name", "loc", "match", "boundary_name", "manager", "designation", "class", "score", "contains", "dist_km", "parts"})
	for _, p := range parks {
		row := []string{p.Ref, p.Name, p.Loc, p.best.confidence(), "", "", "", "", "", "", "", strconv.Itoa(len(p.geom))}
		if c := p.best; c != nil {
			row[4], row[5], row[6], row[7] = c.Unit, c.Manager, c.Designation, c.Layer
			row[8] = strconv.FormatFloat(c.Score, 'f', 2, 64)
			row[9] = strconv.FormatBool(c.Contains)
			row[10] = strconv.FormatFloat(c.DistKm, 'f', 2, 64)
		}
		w.Write(row)
	}
	w.Flush()
	return w.Error()
}

func summarize(parks []*park) {
	byConf := map[string]int{}
	type st struct{ total, matched int }
	byState := map[string]*st{}
	var none, medium []*park
	for _, p := range parks {
		c := p.best.confidence()
		byConf[c]++
		s := byState[p.Loc]
		if s == nil {
			s = &st{}
			byState[p.Loc] = s
		}
		s.total++
		if c != "none" {
			s.matched++
		} else {
			none = append(none, p)
		}
		if c == "medium" {
			medium = append(medium, p)
		}
	}
	n := float64(len(parks))
	fmt.Printf("\n== Match summary: %d parks\n", len(parks))
	for _, c := range []string{"high", "medium", "none"} {
		fmt.Printf("%-7s %6d  %5.1f%%\n", c, byConf[c], 100*float64(byConf[c])/n)
	}
	var states []string
	for s := range byState {
		states = append(states, s)
	}
	sort.Strings(states)
	fmt.Println("\n== By state (matched/total)")
	for _, s := range states {
		fmt.Printf("%s %d/%d  ", s, byState[s].matched, byState[s].total)
	}
	fmt.Println()
	sample := func(title string, ps []*park) {
		fmt.Printf("\n== %s (every %dth)\n", title, max(1, len(ps)/40))
		for i := 0; i < len(ps); i += max(1, len(ps)/40) {
			p := ps[i]
			if c := p.best; c != nil {
				fmt.Printf("%s %s | %s (%.2f, contains=%v, %.1f km)\n", p.Ref, p.Name, c.Unit, c.Score, c.Contains, c.DistKm)
			} else {
				fmt.Printf("%s %s | -\n", p.Ref, p.Name)
			}
		}
	}
	sample("Medium matches", medium)
	sample("No boundary", none)
}
