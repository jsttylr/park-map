// Command fetchtrails finds the route of every US POTA trail park in an
// OpenStreetMap extract and writes each one's lines as gzipped GeoJSON lines
// for scripts/buffer_trails.py to widen into an area.
//
// The extract is the states' Geofabrik files cut down by osmium to route
// relations (hiking, biking, riding, historic and so on) and paths; see the
// build workflow. Each trail park takes the route near its pin whose name best
// matches the park's, climbing to the whole trail when that route is one
// section of a longer one (the Appalachian Trail is a route per state), or,
// with no such route, the paths near its pin that share its name.
//
//	fetchtrails -states                        # Geofabrik names of the states to download
//	fetchtrails -osm us-trails.osm.pbf         # match and write trails.geojsonl.gz
//	fetchtrails -official agency-lines.geojsonl.gz -merge trails.geojsonl.gz -out all-trails.geojsonl.gz
package main

import (
	"bufio"
	"compress/gzip"
	"context"
	"encoding/csv"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log"
	"math"
	"os"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/jsttylr/park-map/internal/match"
	"github.com/paulmach/orb"
	"github.com/paulmach/orb/geojson"
	"github.com/paulmach/osm"
	"github.com/paulmach/osm/osmpbf"
)

const (
	nearKm   = 5  // a route this close to the pin is preferred; paths must be this close
	farKm    = 25 // how far from the pin a route may be to be the park's
	gatherKm = 40 // how far from the pin to gather paths that share the chosen name
	minScore = 0.5
)

// routeTypes are the kinds of route relation a trail park can be.
var routeTypes = map[string]bool{
	"hiking": true, "foot": true, "walking": true, "bicycle": true, "mtb": true, "horse": true,
	"historic": true, "canoe": true, "ski": true, "inline_skates": true, "running": true, "fitness_trail": true,
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

type park struct {
	Ref, Name, Loc string
	Pt             orb.Point
}

type route struct {
	id      int64
	name    string
	ways    []int64
	subs    []int64
	parents []int64
}

type way struct {
	name  string
	nodes []int64
}

type result struct {
	p      *park
	kind   string // "relation" or "paths"
	id     int64
	name   string
	score  float64
	via    string
	lines  orb.MultiLineString
	reason string
}

func main() {
	potaPath := flag.String("pota", "all_parks_ext.csv", "POTA park list CSV")
	prefix := flag.String("prefix", "US-", "park reference prefix to include")
	states := flag.Bool("states", false, "print the Geofabrik names of the states with trail parks, then exit")
	osmPath := flag.String("osm", "us-trails.osm.pbf", "OpenStreetMap extract of routes and paths")
	out := flag.String("out", "trails.geojsonl.gz", "output GeoJSON lines (gzipped)")
	officialPath := flag.String("official", "", "agency trail routes from cmd/fetchlayers; with -merge, use them over the OpenStreetMap routes")
	merge := flag.String("merge", "", "OpenStreetMap routes written earlier by fetchtrails, for -official")
	flag.Parse()

	parks, err := readTrailParks(*potaPath, *prefix)
	if err != nil {
		log.Fatal(err)
	}
	if *officialPath != "" {
		if err := useOfficial(parks, *officialPath, *merge, *out); err != nil {
			log.Fatal(err)
		}
		return
	}
	if *states {
		seen := map[string]bool{}
		for _, p := range parks {
			for _, s := range strings.Split(p.Loc, ",") {
				if g := geofabrik[strings.TrimPrefix(strings.TrimSpace(s), "US-")]; g != "" && !seen[g] {
					seen[g] = true
					fmt.Println(g)
				}
			}
		}
		return
	}
	start := time.Now()
	log.Printf("%d trail parks", len(parks))

	// Parks by the distinctive words of their names, so a route or path name
	// is only compared with parks it shares a word with.
	byWord := map[string][]*park{}
	for _, p := range parks {
		for _, w := range match.Distinctive(p.Name) {
			byWord[w] = append(byWord[w], p)
		}
	}
	type cand struct {
		id    int64
		score float64
	}
	matches := func(name string) []*park {
		seen := map[*park]bool{}
		var out []*park
		for _, w := range match.Distinctive(name) {
			for _, p := range byWord[w] {
				if !seen[p] && match.SharesName(p.Name, name) && !match.Conflicts(p.Name, name) && match.Similarity(p.Name, name) >= minScore {
					seen[p] = true
					out = append(out, p)
				}
			}
		}
		return out
	}

	// Pass 1: route relations, and which parks each one's name matches.
	routes := map[int64]*route{}
	routeCands := map[*park][]cand{}
	err = scan(*osmPath, func(sc *osmpbf.Scanner) { sc.SkipNodes, sc.SkipWays = true, true }, func(o osm.Object) {
		r, ok := o.(*osm.Relation)
		if !ok || !routeTypes[r.Tags.Find("route")] {
			return
		}
		rt := &route{id: int64(r.ID), name: r.Tags.Find("name")}
		for _, m := range r.Members {
			switch m.Type {
			case osm.TypeWay:
				rt.ways = append(rt.ways, m.Ref)
			case osm.TypeRelation:
				rt.subs = append(rt.subs, m.Ref)
			}
		}
		routes[rt.id] = rt
		if rt.name == "" {
			return
		}
		for _, p := range matches(rt.name) {
			routeCands[p] = append(routeCands[p], cand{rt.id, match.Similarity(p.Name, rt.name)})
		}
	})
	if err != nil {
		log.Fatal(err)
	}
	for _, rt := range routes {
		for _, s := range rt.subs {
			if c := routes[s]; c != nil {
				c.parents = append(c.parents, rt.id)
			}
		}
	}
	log.Printf("pass 1: %d routes, %d parks with a route of a matching name (%v)", len(routes), len(routeCands), time.Since(start).Round(time.Second))

	// Climb from a section to the whole trail: a parent route whose name
	// matches about as well ("Appalachian Trail" over "Appalachian Trail -
	// Virginia") stands for it.
	climbed := map[*park]map[int64]string{} // whole trail -> section it was reached from
	needWays := map[int64]bool{}
	for p, cs := range routeCands {
		climbed[p] = map[int64]string{}
		for _, c := range cs {
			id, via := c.id, ""
			for range 4 {
				var up *route
				best := c.score - 0.1
				for _, pid := range routes[id].parents {
					pr := routes[pid]
					if pr == nil || pr.name == "" || !match.SharesName(p.Name, pr.name) || match.Conflicts(p.Name, pr.name) {
						continue
					}
					if s := match.Similarity(p.Name, pr.name); s >= best {
						up, best = pr, s
					}
				}
				if up == nil {
					break
				}
				via, id = routes[id].name, up.id
			}
			climbed[p][id] = via
			for _, w := range wayIDs(routes, id) {
				needWays[w] = true
			}
		}
	}

	// Pass 2: the ways of those routes, and named paths matching a park.
	ways := map[int64]*way{}
	pathCands := map[*park][]int64{}
	err = scan(*osmPath, func(sc *osmpbf.Scanner) { sc.SkipNodes, sc.SkipRelations = true, true }, func(o osm.Object) {
		w, ok := o.(*osm.Way)
		if !ok {
			return
		}
		id := int64(w.ID)
		var ps []*park
		name := w.Tags.Find("name")
		if name != "" && isPath(w.Tags.Find("highway")) {
			ps = matches(name)
		}
		if !needWays[id] && len(ps) == 0 {
			return
		}
		wy := &way{name: name, nodes: make([]int64, len(w.Nodes))}
		for i, n := range w.Nodes {
			wy.nodes[i] = int64(n.ID)
		}
		ways[id] = wy
		for _, p := range ps {
			pathCands[p] = append(pathCands[p], id)
		}
	})
	if err != nil {
		log.Fatal(err)
	}
	needNodes := map[int64]bool{}
	for _, w := range ways {
		for _, n := range w.nodes {
			needNodes[n] = true
		}
	}
	log.Printf("pass 2: %d ways, %d nodes needed (%v)", len(ways), len(needNodes), time.Since(start).Round(time.Second))

	// Pass 3: where those ways' nodes are.
	nodes := make(map[int64]orb.Point, len(needNodes))
	err = scan(*osmPath, func(sc *osmpbf.Scanner) {
		sc.SkipWays, sc.SkipRelations = true, true
		sc.FilterNode = func(n *osm.Node) bool { return needNodes[int64(n.ID)] }
	}, func(o osm.Object) {
		if n, ok := o.(*osm.Node); ok {
			nodes[int64(n.ID)] = orb.Point{n.Lon, n.Lat}
		}
	})
	if err != nil {
		log.Fatal(err)
	}
	log.Printf("pass 3: %d nodes (%v)", len(nodes), time.Since(start).Round(time.Second))
	line := func(id int64) orb.LineString {
		w := ways[id]
		if w == nil {
			return nil
		}
		var l orb.LineString
		for _, n := range w.nodes {
			if pt, ok := nodes[n]; ok {
				l = append(l, pt)
			}
		}
		if len(l) < 2 {
			return nil
		}
		return l
	}

	// Choose each park's route, or failing that its paths.
	var results []*result
	for _, p := range parks {
		r := &result{p: p}
		results = append(results, r)
		bestRank := 0.0
		for id, via := range climbed[p] {
			rt := routes[id]
			var ls orb.MultiLineString
			for _, w := range wayIDs(routes, id) {
				if l := line(w); l != nil {
					ls = append(ls, l)
				}
			}
			km := nearestKm(ls, p.Pt)
			if km > farKm {
				continue
			}
			s := match.Similarity(p.Name, rt.name)
			rank := s
			if km <= nearKm {
				rank += 0.05
			}
			if rank > bestRank {
				bestRank = rank
				r.kind, r.id, r.name, r.score, r.via, r.lines = "relation", id, rt.name, s, via, ls
			}
		}
		if r.lines != nil {
			continue
		}
		// Named paths: the best-named one near the pin, then every path of
		// that name around it.
		var best string
		bestScore := 0.0
		for _, id := range pathCands[p] {
			l := line(id)
			if l == nil {
				continue
			}
			name := ways[id].name
			if s := match.Similarity(p.Name, name); s > bestScore && nearestKm(orb.MultiLineString{l}, p.Pt) <= nearKm {
				best, bestScore, r.id = name, s, id
			}
		}
		if best == "" {
			r.reason = "no route or path of a matching name"
			if len(routeCands[p]) > 0 || len(pathCands[p]) > 0 {
				r.reason = fmt.Sprintf("routes or paths of a matching name, but not near the pin (%d routes, %d paths)", len(routeCands[p]), len(pathCands[p]))
			}
			continue
		}
		for _, id := range pathCands[p] {
			if l := line(id); l != nil && ways[id].name == best && nearestKm(orb.MultiLineString{l}, p.Pt) <= gatherKm {
				r.lines = append(r.lines, l)
			}
		}
		r.kind, r.name, r.score = "paths", best, bestScore
	}

	if err := write(*out, results); err != nil {
		log.Fatal(err)
	}
	drawn := 0
	var missing []*result
	for _, r := range results {
		if r.lines == nil {
			missing = append(missing, r)
			continue
		}
		drawn++
		km := 0.0
		for _, l := range r.lines {
			km += lengthKm(l)
		}
		via := ""
		if r.via != "" {
			via = fmt.Sprintf(" (from %q)", r.via)
		}
		fmt.Printf("%s %s (%s) | %s %d %q%s %.2f, %.0f km, %d lines\n", r.p.Ref, r.p.Name, r.p.Loc, r.kind, r.id, r.name, via, r.score, km, len(r.lines))
	}
	sort.Slice(missing, func(i, j int) bool { return missing[i].p.Ref < missing[j].p.Ref })
	fmt.Printf("\n== No route found (%d)\n", len(missing))
	for _, r := range missing {
		fmt.Printf("%s %s (%s) | %s\n", r.p.Ref, r.p.Name, r.p.Loc, r.reason)
	}
	fmt.Printf("\n== Trail routes: %d of %d trail parks found in %v\n", drawn, len(parks), time.Since(start).Round(time.Second))
}

// scan reads every object of an OSM PBF file, after setup sets what to skip.
func scan(path string, setup func(*osmpbf.Scanner), fn func(osm.Object)) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	sc := osmpbf.New(context.Background(), f, runtime.GOMAXPROCS(0))
	defer sc.Close()
	setup(sc)
	for sc.Scan() {
		fn(sc.Object())
	}
	return sc.Err()
}

// wayIDs returns the ways of a route and of the routes inside it.
func wayIDs(routes map[int64]*route, id int64) []int64 {
	var out []int64
	seen := map[int64]bool{}
	var walk func(int64)
	walk = func(id int64) {
		r := routes[id]
		if r == nil || seen[id] {
			return
		}
		seen[id] = true
		out = append(out, r.ways...)
		for _, s := range r.subs {
			walk(s)
		}
	}
	walk(id)
	return out
}

func isPath(highway string) bool {
	switch highway {
	case "path", "footway", "cycleway", "bridleway", "track", "steps", "pedestrian":
		return true
	}
	return false
}

// nearestKm is how far a point is from the nearest vertex of some lines.
func nearestKm(ls orb.MultiLineString, pt orb.Point) float64 {
	best := math.Inf(1)
	for _, l := range ls {
		for _, q := range l {
			best = math.Min(best, distKm(q, pt))
		}
	}
	return best
}

// write saves each park found with its lines, and each park not found with
// no geometry and the reason, for the report.
func write(path string, results []*result) error {
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()
	gz := gzip.NewWriter(f)
	w := bufio.NewWriter(gz)
	for _, r := range results {
		var b []byte
		if r.lines == nil {
			b, err = json.Marshal(map[string]any{"type": "Feature", "geometry": nil,
				"properties": map[string]any{"ref": r.p.Ref, "missing": r.reason}})
		} else {
			ft := geojson.NewFeature(r.lines)
			ft.Properties["ref"] = r.p.Ref
			ft.Properties["osm"] = fmt.Sprintf("%s/%d", r.kind, r.id)
			ft.Properties["osm_name"] = r.name
			ft.Properties["score"] = r.score
			b, err = json.Marshal(ft)
		}
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
		parks = append(parks, &park{Ref: ref, Name: name, Loc: rec[col["locationDesc"]], Pt: orb.Point{lon, lat}})
	}
	return parks, nil
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
