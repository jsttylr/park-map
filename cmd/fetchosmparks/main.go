// Command fetchosmparks finds a park outline in OpenStreetMap for each US
// POTA park, for the parks PAD-US and the agency maps don't draw: pieces of
// bigger units (Great Falls Park inside the GW Memorial Parkway), city and
// county parks, and places too new to be in PAD-US.
//
// It reads areas exported by osmium from the states' Geofabrik files (see the
// build workflow) and writes, for each park matched, that park's reference
// with the outline, for cmd/builddata -osm to use where it has nothing
// better.
//
//	fetchosmparks -states                              # Geofabrik names of the states to download
//	fetchosmparks -areas us-parks.geojsonseq -out osm-parks.geojsonl.gz
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
	"time"

	"github.com/jsttylr/park-map/internal/match"
	"github.com/paulmach/orb"
	"github.com/paulmach/orb/geojson"
	"github.com/paulmach/orb/planar"
)

const (
	minScore = 0.5  // how well an area's name must match the park's
	trailMin = 0.9  // how well a trail park's name must match, to take an area over its route
	nearKm   = 2.0  // how far from the pin an area may be and still be the park
	gatherKm = 10.0 // how far to gather the other pieces of the chosen area
	kmPerDeg = 111.0
	maxAcres = 2_000_000 // an area bigger than this is a county or a whole forest, not a park
)

type park struct {
	Ref, Name, Loc string
	Pt             orb.Point
	cands          map[string]*cand // the areas near it, by name
	best           *cand
}

// cand is one named area near a park, with every piece of it found.
type cand struct {
	name   string
	score  float64
	inside bool
	km     float64 // how far the nearest piece is from the pin
	geom   orb.MultiPolygon
	ids    []string
	water  bool // a lake or pond, for a fishing lake
}

// add keeps one piece of an area under its name.
func (p *park) add(name, id string, mp orb.MultiPolygon, score float64, inside bool, km float64, water bool) {
	k := match.Key(name)
	c := p.cands[k]
	if c == nil {
		if p.cands == nil {
			p.cands = map[string]*cand{}
		}
		c = &cand{name: name, score: score, km: km, water: water}
		p.cands[k] = c
	}
	c.inside = c.inside || inside
	c.km = min(c.km, km)
	c.geom = append(c.geom, mp...)
	c.ids = append(c.ids, id)
}

// choose picks the area to draw: the one holding the pin if there is one,
// then the best-matching name.
func (p *park) choose() {
	for _, c := range p.cands {
		if acres(c.geom) > maxAcres {
			continue
		}
		b := p.best
		if b == nil || c.inside != b.inside && c.inside || c.inside == b.inside && c.score > b.score {
			p.best = c
		}
	}
}

func main() {
	potaPath := flag.String("pota", "all_parks_ext.csv", "POTA park list CSV")
	prefix := flag.String("prefix", "US-", "park reference prefix to include")
	states := flag.Bool("states", false, "print the Geofabrik names of the states to download, then exit")
	areas := flag.String("areas", "", "areas exported by osmium (GeoJSON lines or sequences), one or more files")
	out := flag.String("out", "osm-parks.geojsonl.gz", "output GeoJSON lines (gzipped)")
	flag.Parse()

	parks, err := readPOTA(*potaPath, *prefix)
	if err != nil {
		log.Fatal(err)
	}
	if *states {
		seen := map[string]bool{}
		var names []string
		for _, p := range parks {
			for _, s := range strings.Split(p.Loc, ",") {
				if g := geofabrik[strings.TrimPrefix(strings.TrimSpace(s), "US-")]; g != "" && !seen[g] {
					seen[g] = true
					names = append(names, g)
				}
			}
		}
		sort.Strings(names)
		for _, n := range names {
			fmt.Println(n)
		}
		return
	}
	start := time.Now()
	log.Printf("%d %s parks", len(parks), *prefix)

	// Parks by the distinctive words of their names, so an area's name is
	// only compared with the parks it shares a word with.
	byWord := map[string][]*park{}
	for _, p := range parks {
		for _, w := range match.Distinctive(p.Name) {
			byWord[w] = append(byWord[w], p)
		}
	}

	files := append([]string{}, flag.Args()...)
	if *areas != "" {
		files = append(files, *areas)
	}
	total, kept := 0, 0
	for _, f := range files {
		err := eachFeature(f, func(name, id string, mp orb.MultiPolygon, water bool) {
			total++
			seen := map[*park]bool{}
			b := mp.Bound()
			for _, w := range match.Distinctive(name) {
				for _, p := range byWord[w] {
					if seen[p] {
						continue
					}
					seen[p] = true
					if !b.Pad(gatherKm/kmPerDeg).Contains(p.Pt) || !match.SharesName(p.Name, name) || match.Conflicts(p.Name, name) || !sameKind(p.Name, name) {
						continue
					}
					// A trail, byway or river park keeps its route unless
					// an area has its own name, and only a fishing lake
					// takes the outline of its lake.
					score := match.Similarity(p.Name, name)
					if score < minScore || match.IsRoute(p.Name) && score < trailMin || water && !fishingLake(p.Name) {
						continue
					}
					// Pieces of the same area are gathered from farther out
					// than the piece that decides the match.
					inside, km := planar.MultiPolygonContains(mp, p.Pt), 0.0
					if !inside {
						km = planar.DistanceFrom(mp, p.Pt) * kmPerDeg
					}
					if !inside && p.cands[match.Key(name)] == nil && km > nearKm {
						continue
					}
					p.add(name, id, mp, score, inside, km, water)
					kept++
				}
			}
		})
		if err != nil {
			log.Fatal(err)
		}
		log.Printf("%s: %d areas read, %d kept so far (%v)", f, total, kept, time.Since(start).Round(time.Second))
	}

	for _, p := range parks {
		p.choose()
	}
	if err := write(*out, parks); err != nil {
		log.Fatal(err)
	}
	n := 0
	for _, p := range parks {
		c := p.best
		if c == nil {
			continue
		}
		n++
		fmt.Printf("%s %s (%s) | %q %s %.2f, %.0f acres, inside=%v, %.2f km, %d pieces\n",
			p.Ref, p.Name, p.Loc, c.name, strings.Join(c.ids[:min(3, len(c.ids))], ","), c.score, acres(c.geom), c.inside, c.km, len(c.geom))
	}
	fmt.Printf("\n== OpenStreetMap outlines: %d of %d parks, from %d areas, in %v\n", n, len(parks), total, time.Since(start).Round(time.Second))
}

func write(path string, parks []*park) error {
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()
	gz := gzip.NewWriter(f)
	w := bufio.NewWriter(gz)
	for _, p := range parks {
		c := p.best
		if c == nil {
			continue
		}
		ft := geojson.NewFeature(round(c.geom))
		ft.Properties["ref"] = p.Ref
		ft.Properties["osm"] = strings.Join(c.ids, ",")
		ft.Properties["osm_name"] = c.name
		ft.Properties["score"] = c.score
		ft.Properties["contains"] = c.inside
		ft.Properties["km"] = math.Round(c.km*100) / 100
		if c.water {
			ft.Properties["water"] = true
		}
		b, err := json.Marshal(ft)
		if err != nil {
			return err
		}
		w.Write(b)
		w.WriteString("\n")
	}
	if err := w.Flush(); err != nil {
		return err
	}
	return gz.Close()
}

// eachFeature streams osmium's GeoJSON output, passing each named polygon
// with its OpenStreetMap id and whether it is water. An airfield's name
// says "Airstrip" for "Airport", as POTA's backcountry airstrips do.
func eachFeature(path string, fn func(name, id string, mp orb.MultiPolygon, water bool)) error {
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
		ft, err := geojson.UnmarshalFeature(line)
		if err != nil || ft.Geometry == nil {
			continue
		}
		name, _ := ft.Properties["name"].(string)
		if name == "" {
			continue
		}
		if a, _ := ft.Properties["aeroway"].(string); a == "aerodrome" || a == "airstrip" {
			name = airstrip.Replace(name)
		}
		water := ft.Properties["natural"] == "water"
		id := "?"
		switch v := ft.ID.(type) {
		case string:
			id = v
		case float64:
			id = strconv.FormatFloat(v, 'f', -1, 64)
		}
		switch g := ft.Geometry.(type) {
		case orb.Polygon:
			fn(name, id, orb.MultiPolygon{g}, water)
		case orb.MultiPolygon:
			fn(name, id, g, water)
		}
	}
	return sc.Err()
}

var airstrip = strings.NewReplacer("Airport", "Airstrip", "Airfield", "Airstrip", "Air Strip", "Airstrip", "Landing Strip", "Airstrip", "Landing Field", "Airstrip")

// fishingLake reports whether a park is a fishing lake ("Meade State
// Fishing Lake"), the water itself.
func fishingLake(name string) bool {
	t := match.Tokens(name)
	return hasPhrase(t, []string{"fishing", "lake"}) || hasPhrase(t, []string{"fishing", "pond"})
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

// acres is roughly how big an area is, to leave out whole counties and
// forests that happen to share a park's name.
func acres(mp orb.MultiPolygon) float64 {
	a := 0.0
	for _, poly := range mp {
		lat := poly.Bound().Center()[1] * math.Pi / 180
		a += planar.Area(poly) * 111320 * math.Cos(lat) * 110574
	}
	return a / 4046.86
}

// round trims coordinates to about a meter, as PAD-US and the trail areas are.
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

// geofabrik names each state's extract at download.geofabrik.de/north-america/us/.
var geofabrik = map[string]string{
	"AL": "alabama", "AK": "alaska", "AZ": "arizona", "AR": "arkansas", "CA": "california",
	"CO": "colorado", "CT": "connecticut", "DE": "delaware", "DC": "district-of-columbia",
	"FL": "florida", "GA": "georgia", "HI": "hawaii", "ID": "idaho", "IL": "illinois",
	"IN": "indiana", "IA": "iowa", "KS": "kansas", "KY": "kentucky", "LA": "louisiana",
	"ME": "maine", "MD": "maryland", "MA": "massachusetts", "MI": "michigan", "MN": "minnesota",
	"MS": "mississippi", "MO": "missouri", "MT": "montana", "NE": "nebraska", "NV": "nevada",
	"NH": "new-hampshire", "NJ": "new-jersey", "NM": "new-mexico", "NY": "new-york",
	"NC": "north-carolina", "ND": "north-dakota", "OH": "ohio", "OK": "oklahoma", "OR": "oregon",
	"PA": "pennsylvania", "RI": "rhode-island", "SC": "south-carolina", "SD": "south-dakota",
	"TN": "tennessee", "TX": "texas", "UT": "utah", "VT": "vermont", "VA": "virginia",
	"WA": "washington", "WV": "west-virginia", "WI": "wisconsin", "WY": "wyoming",
}
