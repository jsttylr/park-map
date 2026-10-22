// Command fetchtrails finds the route of every US POTA trail park in
// OpenStreetMap, through the Overpass API, and writes each one's lines as
// gzipped GeoJSON lines for scripts/buffer_trails.py to widen into an area.
//
// It first lists every named hiking, biking, riding and historic route around
// the trail parks, a few big queries by region, and matches each park to the
// route near its pin whose name best matches the park's, falling back to named
// paths near the pin. It climbs to the whole trail when that route is one
// section of a longer one (the Appalachian Trail is a route per state), and
// downloads only the chosen routes' lines.
package main

import (
	"bufio"
	"bytes"
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
	nearKm   = 5     // a route this close to the pin is preferred
	farKm    = 25    // how far from the pin a route may be to be the park's
	pathsM   = 5000  // how far from the pin to look for named paths
	gatherM  = 40000 // how far to gather paths that share the chosen name
	minScore = 0.5
	tileDeg  = 5 // routes are listed in tiles this many degrees on a side
)

// routeTypes are the kinds of route relation a trail park can be.
const routeTypes = `^(hiking|foot|walking|bicycle|mtb|horse|historic|canoe|ski|inline_skates|running|fitness_trail)$`

var endpoints = []string{
	"https://overpass-api.de/api/interpreter",
	"https://overpass.private.coffee/api/interpreter",
}

type park struct {
	Ref, Name, Loc string
	Lat, Lon       float64
}

type element struct {
	Type   string            `json:"type"`
	ID     int64             `json:"id"`
	Tags   map[string]string `json:"tags"`
	Bounds *struct {
		MinLat float64 `json:"minlat"`
		MinLon float64 `json:"minlon"`
		MaxLat float64 `json:"maxlat"`
		MaxLon float64 `json:"maxlon"`
	} `json:"bounds"`
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
	resume := flag.Bool("resume", false, "keep the parks already in -out and look up only the rest")
	deadline := flag.Duration("deadline", 0, "stop starting new lookups after this long, so a partial result is still saved")
	server := flag.String("overpass", "", "Overpass API URL to use instead of the public ones")
	flag.Parse()
	if *server != "" {
		endpoints = []string{*server}
	}

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
	kept := map[string][]byte{}
	if *resume {
		if kept, err = readKept(*out); err != nil {
			log.Printf("nothing to resume from: %v", err)
		}
	}
	var todo []*park
	for _, p := range parks {
		if kept[p.Ref] == nil {
			todo = append(todo, p)
		}
	}
	log.Printf("%d trail parks, %d already looked up, %d to look up", len(parks), len(parks)-len(todo), len(todo))

	start := time.Now()
	routes, tileErr := listRoutes(todo)
	log.Printf("%d named routes listed in %v", len(routes), time.Since(start).Round(time.Second))

	results := make([]*found, len(todo))
	next := make(chan int)
	var wg sync.WaitGroup
	for w := 0; w < *workers; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := range next {
				if *deadline > 0 && time.Since(start) > *deadline {
					continue // left for the next run
				}
				t := time.Now()
				r := fetch(todo[i], routes, tileErr)
				results[i] = r
				what := r.reason
				if len(r.lines) > 0 {
					what = fmt.Sprintf("%s %d %q, %d lines", r.kind, r.id, r.name, len(r.lines))
				}
				log.Printf("%s %s: %s (%v)", r.p.Ref, r.p.Name, what, time.Since(t).Round(time.Second))
			}
		}()
	}
	for i := range todo {
		next <- i
		if (i+1)%25 == 0 {
			log.Printf("%d of %d parks in %v", i+1, len(todo), time.Since(start).Round(time.Second))
		}
	}
	close(next)
	wg.Wait()

	tmp := *out + ".tmp"
	f, err := os.Create(tmp)
	if err != nil {
		log.Fatal(err)
	}
	gz := gzip.NewWriter(f)
	w := bufio.NewWriter(gz)
	drawn, none := 0, 0
	for _, p := range parks {
		if line := kept[p.Ref]; line != nil {
			w.Write(line)
			w.WriteString("\n")
			if bytes.Contains(line, []byte(`"geometry":null`)) {
				none++
			} else {
				drawn++
			}
		}
	}
	byKind := map[string]int{}
	var missing []*found
	unchecked := 0
	for _, r := range results {
		if r == nil {
			unchecked++
			continue
		}
		if len(r.lines) == 0 {
			missing = append(missing, r)
			if strings.HasPrefix(r.reason, "Overpass:") {
				unchecked++
				continue // try again next run
			}
			// Remember a park with no route so the next run skips it too.
			b, _ := json.Marshal(map[string]any{"type": "Feature", "geometry": nil,
				"properties": map[string]any{"ref": r.p.Ref, "missing": r.reason}})
			w.Write(b)
			w.WriteString("\n")
			none++
			continue
		}
		byKind[r.kind]++
		drawn++
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
	if err := os.Rename(tmp, *out); err != nil {
		log.Fatal(err)
	}

	sort.Slice(missing, func(i, j int) bool { return missing[i].p.Ref < missing[j].p.Ref })
	fmt.Printf("\n== No route found this run (%d)\n", len(missing))
	for _, r := range missing {
		fmt.Printf("%s %s (%s) | %s\n", r.p.Ref, r.p.Name, r.p.Loc, r.reason)
	}
	fmt.Printf("\n== Trail routes: %d of %d parks have one, %d have none, %d still to look up (this run: %d relations, %d named paths, %v)\n",
		drawn, len(parks), none, unchecked, byKind["relation"], byKind["paths"], time.Since(start).Round(time.Second))
	if unchecked > 0 {
		os.Exit(3)
	}
}

// readKept returns the saved line of every park in a previous output.
func readKept(path string) (map[string][]byte, error) {
	f, err := os.Open(path)
	if err != nil {
		return map[string][]byte{}, err
	}
	defer f.Close()
	gz, err := gzip.NewReader(f)
	if err != nil {
		return map[string][]byte{}, err
	}
	kept := map[string][]byte{}
	sc := bufio.NewScanner(gz)
	sc.Buffer(make([]byte, 1<<20), 1<<30)
	for sc.Scan() {
		var ft struct {
			Properties struct {
				Ref string `json:"ref"`
			} `json:"properties"`
		}
		if json.Unmarshal(sc.Bytes(), &ft) == nil && ft.Properties.Ref != "" {
			kept[ft.Properties.Ref] = append([]byte(nil), sc.Bytes()...)
		}
	}
	return kept, sc.Err()
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

// tileOf names the listing tile a point falls in.
func tileOf(lat, lon float64) [2]int {
	return [2]int{int(math.Floor(lat / tileDeg)), int(math.Floor(lon / tileDeg))}
}

// tilesNear returns the tiles within farKm of a park's pin.
func tilesNear(p *park) map[[2]int]bool {
	d := farKm / 111.0
	out := map[[2]int]bool{}
	for _, dl := range []float64{-d, d} {
		for _, dn := range []float64{-d / math.Cos(p.Lat*math.Pi/180), d / math.Cos(p.Lat*math.Pi/180)} {
			out[tileOf(p.Lat+dl, p.Lon+dn)] = true
		}
	}
	return out
}

// listRoutes lists the named routes in every tile near a park, without
// their lines. Tiles the server couldn't list are returned with their error.
func listRoutes(parks []*park) ([]element, map[[2]int]error) {
	tiles := map[[2]int]bool{}
	for _, p := range parks {
		for t := range tilesNear(p) {
			tiles[t] = true
		}
	}
	var mu sync.Mutex
	var routes []element
	seen := map[int64]bool{}
	errs := map[[2]int]error{}
	next := make(chan [2]int)
	var wg sync.WaitGroup
	for w := 0; w < 2; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for t := range next {
				s, w := float64(t[0]*tileDeg), float64(t[1]*tileDeg)
				q := fmt.Sprintf(`[out:json][timeout:300];rel[type~"^(route|superroute)$"][route~"%s"][name](%g,%g,%g,%g);out tags bb;`,
					routeTypes, s, w, s+tileDeg, w+tileDeg)
				els, err := overpass(q)
				mu.Lock()
				if err != nil {
					errs[t] = err
					log.Printf("routes in tile %v: %v", t, err)
				}
				for _, e := range els {
					if !seen[e.ID] {
						seen[e.ID] = true
						routes = append(routes, e)
					}
				}
				mu.Unlock()
			}
		}()
	}
	for t := range tiles {
		next <- t
	}
	close(next)
	wg.Wait()
	log.Printf("listed routes in %d tiles, %d failed", len(tiles), len(errs))
	return routes, errs
}

// fetch finds one park's route and downloads its lines.
func fetch(p *park, routes []element, tileErr map[[2]int]error) *found {
	r := &found{p: p}
	for t := range tilesNear(p) {
		if err := tileErr[t]; err != nil {
			r.reason = "Overpass: " + err.Error()
			return r
		}
	}
	ll := fmt.Sprintf("%.6f,%.6f", p.Lat, p.Lon)
	// Routes whose box comes within farKm of the pin, nearer ones first.
	var cands []element
	var chosen *element
	bestRank := 0.0
	for i, e := range routes {
		km := boxKm(e, p)
		if km > farKm {
			continue
		}
		cands = append(cands, e)
		name := e.Tags["name"]
		s := match.Similarity(p.Name, name)
		if s < minScore || !match.SharesName(p.Name, name) {
			continue
		}
		rank := s
		if km <= nearKm {
			rank += 0.05
		}
		if rank > bestRank {
			chosen, bestRank = &routes[i], rank
		}
	}
	if chosen == nil {
		// No route: named paths near the pin.
		els, err := overpass(fmt.Sprintf(`[out:json][timeout:180];way(around:%d,%s)[highway~"^(path|footway|cycleway|bridleway|track|steps|pedestrian)$"][name];out tags;`, pathsM, ll))
		if err != nil {
			r.reason = "Overpass: " + err.Error()
			return r
		}
		chosen = pick(p, els)
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
		els, err := overpass(fmt.Sprintf(`[out:json][timeout:300];way(around:%d,%s)[name="%s"];out geom;`, gatherM, ll, name))
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

// boxKm is roughly how far a park's pin is from a route's bounding box.
func boxKm(e element, p *park) float64 {
	b := e.Bounds
	if b == nil {
		return math.Inf(1)
	}
	dy := math.Max(0, math.Max(b.MinLat-p.Lat, p.Lat-b.MaxLat)) * 111
	dx := math.Max(0, math.Max(b.MinLon-p.Lon, p.Lon-b.MaxLon)) * 111 * math.Cos(p.Lat*math.Pi/180)
	return math.Hypot(dx, dy)
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

var client = &http.Client{Timeout: 6 * time.Minute}

// overpass runs one query, waiting and retrying when the server is busy.
func overpass(q string) ([]element, error) {
	var last error
	for try := 0; try < 4; try++ {
		if try > 0 {
			time.Sleep(time.Duration(15<<(try-1)) * time.Second)
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
