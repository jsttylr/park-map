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
	combinedKm = 800.0 // the same for the other units a combined name names
	kmPerDeg   = 111.0
	minHigh    = 0.5
	minMedium  = 0.3
	minNearMed = 0.75
)

type park struct {
	Ref, Name, Loc string
	Pt             orb.Point
	best           *candidate
	cands          map[string]*candidate // best candidate per boundary key
	otherState     int                   // parcels of its unit left out for being in another state
	otherGeom      orb.MultiPolygon      // those parcels, used only if none are in its own states
	combined       map[string]bool       // other units its combined name names
	geom           orb.MultiPolygon
}

type candidate struct {
	Key, Unit, Manager, Designation, Layer string
	Score, DistKm                          float64
	Contains                               bool
	Route                                  bool // a trail drawn along its OpenStreetMap route
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
	case c.Route:
		return "route"
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
	show := flag.String("show", "", "comma-separated references whose candidates to print")
	trails := flag.String("trails", "", "trail areas from scripts/buffer_trails.py (GeoJSON lines), used instead of PAD-US for those parks")
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

	shown := map[string]bool{}
	for _, r := range strings.Split(*show, ",") {
		shown[strings.TrimSpace(r)] = true
	}
	nearby := map[*park][]string{}

	// Pass 1: score every polygon near each park point.
	for _, l := range layers {
		n := 0
		err := eachFeature(l, func(f *geojson.Feature, mp orb.MultiPolygon) {
			n++
			b := mp.Bound().Pad(nearKm / kmPerDeg)
			for _, p := range ix.within(b) {
				if shown[p.Ref] {
					d := 0.0
					if !planar.MultiPolygonContains(mp, p.Pt) {
						d = planar.DistanceFrom(mp, p.Pt) * kmPerDeg
					}
					nearby[p] = append(nearby[p], fmt.Sprintf("%5.1f km  %s / %s [%s, %s, %s, %s]", d,
						prop(f, "Unit_Nm"), prop(f, "Loc_Nm"), prop(f, "Mang_Name"), prop(f, "Des_Tp"), prop(f, "FeatClass"), prop(f, "State_Nm")))
				}
				c := score(f, l, mp, p)
				if c == nil {
					continue
				}
				if p.cands == nil {
					p.cands = map[string]*candidate{}
				}
				if old := p.cands[c.Key]; old == nil || c.rank() > old.rank() {
					p.cands[c.Key] = c
				}
			}
		})
		if err != nil {
			log.Fatal(err)
		}
		log.Printf("pass 1: %s, %d features", l, n)
	}

	for p, lines := range nearby {
		sort.Strings(lines)
		fmt.Printf("\n== Everything within %.0f km of %s %s\n", nearKm, p.Ref, p.Name)
		for _, l := range lines {
			fmt.Println("  " + l)
		}
	}
	choose(parks, *show)

	// Pass 2: collect every parcel of each chosen unit.
	otherState := 0
	want := map[string][]*park{}
	byPart := map[string][]*park{} // matched parks with combined names, by each unit they name
	for _, p := range parks {
		if p.best.confidence() != "none" {
			want[p.best.Key] = append(want[p.best.Key], p)
			for _, k := range match.Parts(p.Name) {
				byPart[k] = append(byPart[k], p)
			}
		}
	}
	for _, l := range layers {
		err := eachFeature(l, func(f *geojson.Feature, mp orb.MultiPolygon) {
			var ps []*park
			reach := map[*park]float64{}
			combinedUnit := map[*park]string{}
			for _, k := range []string{unitKey(f), locKey(f)} {
				if k != "" {
					ps = append(ps, want[k]...)
				}
			}
			// A park named for several units ("George Washington and
			// Jefferson National Forest") also takes the other units it
			// names, when the same agency manages them.
			if unit := prop(f, "Unit_Nm"); unit != "" {
				for _, p := range byPart[match.SingularKey(unit)] {
					if p.best.Key != unitKey(f) && p.best.Manager == prop(f, "Mang_Name") && p.best.Layer == prop(f, "FeatClass") {
						ps = append(ps, p)
						reach[p] = combinedKm
						combinedUnit[p] = unit
					}
				}
			}
			if len(ps) == 0 {
				return
			}
			b := mp.Bound()
			st := prop(f, "State_Nm")
			for _, p := range ps {
				km := gatherKm
				if r := reach[p]; r > 0 {
					km = r
				}
				if !b.Pad(km / kmPerDeg).Contains(p.Pt) {
					continue
				}
				// A park only takes the parcels in its own states, so the
				// Kentucky piece of a national forest doesn't pull in the
				// Virginia piece.
				if len(st) == 2 && !strings.Contains(p.Loc, "US-"+strings.ToUpper(st)) {
					otherState++
					p.otherState++
					p.otherGeom = append(p.otherGeom, mp...)
					continue
				}
				p.geom = append(p.geom, mp...)
				if u := combinedUnit[p]; u != "" {
					if p.combined == nil {
						p.combined = map[string]bool{}
					}
					p.combined[u] = true
				}
			}
		})
		if err != nil {
			log.Fatal(err)
		}
		log.Printf("pass 2: %s, %d parcels left out for being in another state", l, otherState)
	}

	// A park right on a state line can match a unit whose parcels are all
	// over the line (Copan Lake, Beach Pond); it keeps them rather than
	// ending up with no boundary.
	for _, p := range parks {
		if len(p.geom) == 0 {
			p.geom = p.otherGeom
		}
		p.otherGeom = nil
	}

	if *trails != "" {
		n, err := useTrails(*trails, parks)
		if err != nil {
			log.Fatal(err)
		}
		log.Printf("trails: %d parks drawn along their route", n)
	}

	fmt.Println("\n== Parks that also took other units their name names")
	for _, p := range parks {
		if len(p.combined) > 0 {
			var us []string
			for u := range p.combined {
				us = append(us, u)
			}
			sort.Strings(us)
			fmt.Printf("%s %s (%s) | %s + %s\n", p.Ref, p.Name, p.Loc, p.best.Unit, strings.Join(us, "; "))
		}
	}

	fmt.Println("\n== Parks that left out parcels of their unit in other states (kept/left out)")
	for _, p := range parks {
		if p.otherState > 0 {
			fmt.Printf("%s %s (%s) | %s %d/%d\n", p.Ref, p.Name, p.Loc, p.best.Unit, len(p.geom), p.otherState)
		}
	}

	if err := writeGeoJSON(*out, parks); err != nil {
		log.Fatal(err)
	}
	if err := writeReport(*report, parks); err != nil {
		log.Fatal(err)
	}
	summarize(parks)
}

// useTrails gives each trail park found in OpenStreetMap the area along its
// route in place of its PAD-US outline: POTA counts a trail as the route and
// 100 ft either side, not the land around it.
func useTrails(path string, parks []*park) (int, error) {
	byRef := map[string]*park{}
	for _, p := range parks {
		byRef[p.Ref] = p
	}
	n := 0
	err := eachFeature(path, func(f *geojson.Feature, mp orb.MultiPolygon) {
		p := byRef[prop(f, "ref")]
		if p == nil {
			return
		}
		// Land named after a trail ("Scioto Trail State Forest") keeps a
		// good PAD-US outline; only the trail itself is drawn as a band.
		if p.best.confidence() == "high" && !match.EndsInTrail(p.Name) {
			return
		}
		score, _ := f.Properties["score"].(float64)
		p.geom = mp
		p.best = &candidate{
			Key: "osm:" + prop(f, "osm"), Unit: prop(f, "osm_name"), Manager: "OSM",
			Designation: "TRAIL", Layer: "route", Score: score, Contains: true, Route: true,
		}
		n++
	})
	return n, err
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

// dominated is how far below the best-named park another park's name score
// may be and still share that boundary. A park whose distinctive words are
// all in the boundary's name shares it regardless ("White River National
// Wildlife Refuge" and "Dale Bumpers White River National Wildlife Refuge").
const dominated = 0.25

// choose picks each park's boundary. A boundary another park matches by a
// clearly better name belongs to that park, so a park that only shares a word
// with it falls back to its next candidate, or to no boundary, instead of
// taking a whole national forest.
func choose(parks []*park, show string) {
	top := map[string]float64{}
	who := map[string]*park{}
	for _, p := range parks {
		for k, c := range p.cands {
			if c.confidence() != "none" && c.Score > top[k] {
				top[k], who[k] = c.Score, p
			}
		}
	}
	shown := map[string]bool{}
	for _, r := range strings.Split(show, ",") {
		shown[strings.TrimSpace(r)] = true
	}
	var displaced []string
	for _, p := range parks {
		var cs []*candidate
		for _, c := range p.cands {
			cs = append(cs, c)
		}
		sort.Slice(cs, func(i, j int) bool { return cs[i].rank() > cs[j].rank() })
		var first *candidate
		for _, c := range cs {
			if first == nil {
				first = c
			}
			if c.Score >= top[c.Key]-dominated || match.CoreWithin(p.Name, c.Unit) {
				p.best = c
				break
			}
		}
		if first != nil && p.best != first && first.confidence() != "none" {
			owner := who[first.Key]
			to := "no boundary"
			if p.best != nil {
				to = p.best.Unit
			}
			displaced = append(displaced, fmt.Sprintf("%s %s | gave up %s (%.2f) to %s %s (%.2f), now %s",
				p.Ref, p.Name, first.Unit, first.Score, owner.Ref, owner.Name, top[first.Key], to))
		}
		if shown[p.Ref] {
			fmt.Printf("\n== Candidates for %s %s (%s, %.5f,%.5f)\n", p.Ref, p.Name, p.Loc, p.Pt[1], p.Pt[0])
			for _, c := range cs {
				o := who[c.Key]
				ref := ""
				if o != nil {
					ref = o.Ref
				}
				fmt.Printf("  %-6s %.2f contains=%v %.1f km | %s [%s, %s, %s] best name match %s %.2f\n",
					c.confidence(), c.Score, c.Contains, c.DistKm, c.Unit, c.Manager, c.Designation, c.Layer, ref, top[c.Key])
			}
		}
	}
	sort.Strings(displaced)
	fmt.Printf("\n== %d parks gave up a boundary to a park with a better-matching name\n", len(displaced))
	for _, d := range displaced {
		fmt.Println(d)
	}
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

// locKey names the local piece of a unit (Loc_Nm), such as one trail or
// tract inside a national forest, so a park matched by that name collects only
// that piece and not the whole forest.
func locKey(f *geojson.Feature) string {
	l := prop(f, "Loc_Nm")
	if l == "" {
		return ""
	}
	return "loc:" + match.Key(l) + "|" + unitKey(f)
}

func score(f *geojson.Feature, layer string, mp orb.MultiPolygon, p *park) *candidate {
	unit := prop(f, "Unit_Nm")
	if unit == "" {
		return nil
	}
	s, key, name := 0.0, unitKey(f), unit
	if match.SharesName(p.Name, unit) {
		s = match.Similarity(p.Name, unit)
	}
	for _, part := range match.Parts(p.Name) {
		if part == match.SingularKey(unit) {
			s = max(s, 0.95)
		}
	}
	if loc := prop(f, "Loc_Nm"); loc != "" && match.Key(loc) != match.Key(unit) && match.SharesName(p.Name, loc) {
		if ls := match.Similarity(p.Name, loc); ls > s {
			s, key, name = ls, locKey(f), loc
		}
	}
	if s < minMedium || match.IsTrail(p.Name) && !match.IsTrail(name) {
		return nil
	}
	c := &candidate{
		Key: key, Unit: name, Layer: prop(f, "FeatClass"), Score: s,
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
	w.Write([]string{"ref", "name", "loc", "lat", "lon", "match", "boundary_name", "manager", "designation", "class", "score", "contains", "dist_km", "parts"})
	for _, p := range parks {
		lat := strconv.FormatFloat(p.Pt[1], 'f', 5, 64)
		lon := strconv.FormatFloat(p.Pt[0], 'f', 5, 64)
		row := []string{p.Ref, p.Name, p.Loc, lat, lon, p.best.confidence(), "", "", "", "", "", "", "", strconv.Itoa(len(p.geom))}
		if c := p.best; c != nil {
			row[6], row[7], row[8], row[9] = c.Unit, c.Manager, c.Designation, c.Layer
			row[10] = strconv.FormatFloat(c.Score, 'f', 2, 64)
			row[11] = strconv.FormatBool(c.Contains)
			row[12] = strconv.FormatFloat(c.DistKm, 'f', 2, 64)
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
	for _, c := range []string{"route", "high", "medium", "none"} {
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
