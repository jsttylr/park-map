// Command fetchtrails finds the route of every US POTA trail park in
// OpenStreetMap, through the Overpass API, and writes each one's lines as
// gzipped GeoJSON lines for scripts/buffer_trails.py to widen into an area.
//
// For each trail park it lists the named route relations and paths within a
// few km of the park's pin, takes the one whose name best matches the park's,
// climbs to the whole trail when that route is one section of a longer one
// (the Appalachian Trail is a route per state), and downloads its lines.
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
	"net/http"
	"net/url"
	"os"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/jsttylr/park-map/internal/match"
	"github.com/paulmach/orb"
	"github.com/paulmach/orb/geojson"
)

const (
	nearM    = 5000  // how far from the pin a route may be to be the park's
	farM     = 25000 // second look for routes only, when nothing is that near
	pathsM   = 40000 // how far to gather paths that share the chosen name
	minScore = 0.5
)

var endpoints = []string{
	"https://overpass-api.de/api/interpreter",
	"https://overpass.private.coffee/api/interpreter",
}

type park struct {
	Ref, Name, Loc string
	Lat, Lon       float64
}

type element struct {
	Type     string            `json:"type"`
	ID       int64             `json:"id"`
	Tags     map[string]string `json:"tags"`
	Geometry []struct {
		Lat float64 `json:"lat"`
		Lon float64 `json:"lon"`
	} `json:"geometry"`
}

type found struct {
	p       *park
	kind    string // "relation" or "paths"
	id      int64
	name    string
	score   float64
	lines   orb.MultiLineString
	reason  string
	climbed []string
}

func main() {
	potaPath := flag.String("pota", "all_parks_ext.csv", "POTA park list CSV")
	prefix := flag.String("prefix", "US-", "park reference prefix to include")
	out := flag.String("out", "trails.geojsonl.gz", "output GeoJSON lines (gzipped)")
	only := flag.String("only", "", "comma-separated references to fetch (default all trail parks)")
	workers := flag.Int("workers", 2, "parallel Overpass requests")
	flag.Parse()

	parks, err := readTrailParks(*potaPath, *prefix)
	if err != nil {
		log.Fatal(err)
	}
	if *only != "" {
		want := map[string]bool{}
		for _, r := range strings.Split(*only, ",") {
			want[strings.TrimSpace(r)] = true
		}
		var keep []*park
		for _, p := range parks {
			if want[p.Ref] {
				keep = append(keep, p)
			}
		}
		parks = keep
	}
	log.Printf("%d trail parks", len(parks))

	results := make([]*found, len(parks))
	next := make(chan int)
	var wg sync.WaitGroup
	for w := 0; w < *workers; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := range next {
				results[i] = fetch(parks[i])
			}
		}()
	}
	start := time.Now()
	for i := range parks {
		next <- i
		if (i+1)%25 == 0 {
			log.Printf("%d of %d parks in %v", i+1, len(parks), time.Since(start).Round(time.Second))
		}
	}
	close(next)
	wg.Wait()

	f, err := os.Create(*out)
	if err != nil {
		log.Fatal(err)
	}
	gz := gzip.NewWriter(f)
	w := bufio.NewWriter(gz)
	byKind := map[string]int{}
	var missing []*found
	for _, r := range results {
		if len(r.lines) == 0 {
			missing = append(missing, r)
			continue
		}
		byKind[r.kind]++
		ft := geojson.NewFeature(r.lines)
		ft.Properties["ref"] = r.p.Ref
		ft.Properties["osm"] = fmt.Sprintf("%s/%d", r.kind, r.id)
		ft.Properties["osm_name"] = r.name
		ft.Properties["score"] = r.score
		b, err := json.Marshal(ft)
		if err != nil {
			log.Fatal(err)
		}
		w.Write(b)
		w.WriteString("\n")
		km := 0.0
		for _, l := range r.lines {
			km += lengthKm(l)
		}
		climb := ""
		if len(r.climbed) > 0 {
			climb = " via " + strings.Join(r.climbed, " < ")
		}
		fmt.Printf("%s %s (%s) | %s %d %q%s (%.2f) %.0f km, %d lines\n",
			r.p.Ref, r.p.Name, r.p.Loc, r.kind, r.id, r.name, climb, r.score, km, len(r.lines))
	}
	if err := w.Flush(); err != nil {
		log.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		log.Fatal(err)
	}
	f.Close()

	sort.Slice(missing, func(i, j int) bool { return missing[i].p.Ref < missing[j].p.Ref })
	fmt.Printf("\n== No route found (%d)\n", len(missing))
	for _, r := range missing {
		fmt.Printf("%s %s (%s) | %s\n", r.p.Ref, r.p.Name, r.p.Loc, r.reason)
	}
	fmt.Printf("\n== Trail routes: %d of %d parks (%d relations, %d named paths) in %v\n",
		len(parks)-len(missing), len(parks), byKind["relation"], byKind["paths"], time.Since(start).Round(time.Second))
	failed := 0
	for _, r := range missing {
		if strings.HasPrefix(r.reason, "Overpass:") {
			failed++
		}
	}
	if failed > 0 {
		// The file is still usable, but shouldn't be kept as this month's.
		log.Printf("%d parks not looked up because Overpass failed", failed)
		os.Exit(3)
	}
}

func readTrailParks(path, prefix string) ([]*park, error) {
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
		ref, name := rec[col["reference"]], rec[col["name"]]
		if !strings.HasPrefix(ref, prefix) || rec[col["active"]] != "1" || !match.IsTrail(name) {
			continue
		}
		lat, err1 := strconv.ParseFloat(rec[col["latitude"]], 64)
		lon, err2 := strconv.ParseFloat(rec[col["longitude"]], 64)
		if err1 != nil || err2 != nil {
			continue
		}
		parks = append(parks, &park{Ref: ref, Name: name, Loc: rec[col["locationDesc"]], Lat: lat, Lon: lon})
	}
	return parks, nil
}

// fetch finds one park's route and downloads its lines.
func fetch(p *park) *found {
	r := &found{p: p}
	ll := fmt.Sprintf("%.6f,%.6f", p.Lat, p.Lon)
	var cands []element
	var chosen *element
	for _, q := range []string{
		// Route relations using any way near the pin, and named paths there.
		fmt.Sprintf(`[out:json][timeout:120];way(around:%d,%s)->.w;(rel(bw.w)[type=route][name];way.w[highway~"^(path|footway|cycleway|bridleway|track|steps|pedestrian)$"][name];);out tags;`, nearM, ll),
		// Farther out, routes only: a pin at a visitor center off the trail.
		fmt.Sprintf(`[out:json][timeout:120];way(around:%d,%s)->.w;rel(bw.w)[type=route][name];out tags;`, farM, ll),
	} {
		els, err := overpass(q)
		if err != nil {
			r.reason = "Overpass: " + err.Error()
			return r
		}
		if chosen = pick(p, els); chosen != nil {
			break
		}
		cands = append(cands, els...)
	}
	if chosen == nil {
		r.reason = fmt.Sprintf("no route or path near the pin matches the name (%s)", closest(p, cands))
		return r
	}
	c := *chosen
	r.name, r.score = c.Tags["name"], match.Similarity(p.Name, c.Tags["name"])
	if c.Type == "relation" {
		r.kind, r.id = "relation", c.ID
		// Climb to the whole trail when this route is one section of it.
		for range 4 {
			els, err := overpass(fmt.Sprintf(`[out:json][timeout:120];rel(%d);rel(br)[name];out tags;`, r.id))
			if err != nil {
				break
			}
			var up *element
			for i, e := range els {
				if s := match.Similarity(p.Name, e.Tags["name"]); s >= r.score-0.1 && match.SharesName(p.Name, e.Tags["name"]) {
					if up == nil || s > match.Similarity(p.Name, up.Tags["name"]) {
						up = &els[i]
					}
				}
			}
			if up == nil {
				break
			}
			r.climbed = append(r.climbed, r.name)
			r.id, r.name, r.score = up.ID, up.Tags["name"], match.Similarity(p.Name, up.Tags["name"])
		}
		els, err := overpass(fmt.Sprintf(`[out:json][timeout:600][maxsize:1073741824];rel(%d);>>;way._;out geom;`, r.id))
		if err != nil {
			r.reason = "Overpass: " + err.Error()
			return r
		}
		r.lines = lines(els)
	} else {
		r.kind, r.id = "paths", c.ID
		name := strings.NewReplacer(`\`, `\\`, `"`, `\"`).Replace(c.Tags["name"])
		els, err := overpass(fmt.Sprintf(`[out:json][timeout:300];way(around:%d,%s)[name="%s"];out geom;`, pathsM, ll, name))
		if err != nil {
			r.reason = "Overpass: " + err.Error()
			return r
		}
		r.lines = lines(els)
	}
	if len(r.lines) == 0 {
		r.reason = fmt.Sprintf("%s %d %q has no lines", r.kind, r.id, r.name)
	}
	return r
}

// pick returns the candidate whose name best matches the park's, preferring a
// route relation over a lone path of about the same name.
func pick(p *park, els []element) *element {
	var best *element
	bestRank := 0.0
	for i, e := range els {
		name := e.Tags["name"]
		s := match.Similarity(p.Name, name)
		if s < minScore || !match.SharesName(p.Name, name) {
			continue
		}
		rank := s
		if e.Type == "relation" {
			rank += 0.1
		}
		if rank > bestRank {
			best, bestRank = &els[i], rank
		}
	}
	return best
}

// closest names the best-scoring candidate that wasn't good enough, for the log.
func closest(p *park, els []element) string {
	best, bs := "", 0.0
	for _, e := range els {
		if s := match.Similarity(p.Name, e.Tags["name"]); s > bs {
			best, bs = e.Tags["name"], s
		}
	}
	if best == "" {
		return "nothing named nearby"
	}
	return fmt.Sprintf("closest %q %.2f", best, bs)
}

func lines(els []element) orb.MultiLineString {
	var out orb.MultiLineString
	seen := map[int64]bool{}
	for _, e := range els {
		if e.Type != "way" || len(e.Geometry) < 2 || seen[e.ID] {
			continue
		}
		seen[e.ID] = true
		l := make(orb.LineString, len(e.Geometry))
		for i, g := range e.Geometry {
			l[i] = orb.Point{g.Lon, g.Lat}
		}
		out = append(out, l)
	}
	return out
}

func lengthKm(l orb.LineString) float64 {
	km := 0.0
	for i := 1; i < len(l); i++ {
		km += distKm(l[i-1], l[i])
	}
	return km
}

func distKm(a, b orb.Point) float64 {
	const rad = math.Pi / 180
	la1, la2 := a[1]*rad, b[1]*rad
	s1, s2 := math.Sin((la2-la1)/2), math.Sin((b[0]-a[0])*rad/2)
	h := s1*s1 + math.Cos(la1)*math.Cos(la2)*s2*s2
	return 2 * 6371 * math.Asin(math.Sqrt(h))
}

var client = &http.Client{Timeout: 11 * time.Minute}

// overpass runs one query, waiting and retrying when the server is busy.
func overpass(q string) ([]element, error) {
	var last error
	for try := 0; try < 6; try++ {
		if try > 0 {
			time.Sleep(time.Duration(10<<min(try-1, 4)) * time.Second)
		}
		ep := endpoints[try%len(endpoints)]
		req, _ := http.NewRequest("POST", ep, strings.NewReader("data="+url.QueryEscape(q)))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		req.Header.Set("User-Agent", "park-map trail fetcher (github.com/jsttylr/park-map)")
		resp, err := client.Do(req)
		if err != nil {
			last = err
			continue
		}
		var body struct {
			Elements []element `json:"elements"`
			Remark   string    `json:"remark"`
		}
		err = json.NewDecoder(resp.Body).Decode(&body)
		resp.Body.Close()
		if resp.StatusCode != 200 {
			last = fmt.Errorf("%s: %s", ep, resp.Status)
			continue
		}
		if err != nil {
			last = fmt.Errorf("%s: %v", ep, err)
			continue
		}
		if strings.Contains(body.Remark, "runtime error") {
			last = fmt.Errorf("%s: %s", ep, body.Remark)
			continue
		}
		return body.Elements, nil
	}
	return nil, last
}
