package main

import (
	"bufio"
	"compress/gzip"
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strings"

	"github.com/jsttylr/park-map/internal/match"
	"github.com/paulmach/orb"
	"github.com/paulmach/orb/geojson"
)

const (
	officialScore = 0.9
	officialKm    = 300
	alongScore    = 0.7 // for a byway or river
	alongKm       = 25
	joinKm        = 2 // pieces of one route this close are joined
)

// official is one agency's route for a trail, from cmd/fetchlayers.
type official struct {
	name, source, manager string
	lines                 orb.MultiLineString
	parts                 []orb.MultiLineString // each feature's lines
}

// near keeps the pieces of a route that reach a pin: those within km of it
// and those joined end to end to them. Two rivers of one name (the Salmon in
// Idaho and in Oregon) are different parks.
func (o *official) near(pt orb.Point, km float64) orb.MultiLineString {
	var kept orb.MultiLineString
	used := make([]bool, len(o.parts))
	for i, p := range o.parts {
		if nearestKm(p, pt) <= km {
			kept, used[i] = append(kept, p...), true
		}
	}
	for grew := len(kept) > 0; grew; {
		grew = false
		for i, p := range o.parts {
			if !used[i] && joins(p, kept) {
				kept, used[i], grew = append(kept, p...), true, true
			}
		}
	}
	return kept
}

// joins reports whether a piece of route starts or ends at another.
func joins(p, kept orb.MultiLineString) bool {
	for _, l := range p {
		if len(l) > 0 && (nearestKm(kept, l[0]) <= joinKm || nearestKm(kept, l[len(l)-1]) <= joinKm) {
			return true
		}
	}
	return false
}

// readOfficial gathers the routes in a fetchlayers lines file by name.
func readOfficial(path string) (map[string]*official, error) {
	out := map[string]*official{}
	err := eachLine(path, func(b []byte) error {
		f, err := geojson.UnmarshalFeature(b)
		if err != nil || f.Geometry == nil {
			return nil
		}
		name, _ := f.Properties["Unit_Nm"].(string)
		if name == "" {
			return nil
		}
		o := out[name]
		if o == nil {
			src, _ := f.Properties["Source"].(string)
			mgr, _ := f.Properties["Mang_Name"].(string)
			o = &official{name: name, source: src, manager: mgr}
			out[name] = o
		}
		var ml orb.MultiLineString
		switch g := f.Geometry.(type) {
		case orb.LineString:
			ml = orb.MultiLineString{g}
		case orb.MultiLineString:
			ml = g
		}
		o.lines = append(o.lines, ml...)
		o.parts = append(o.parts, ml)
		return nil
	})
	return out, err
}

// useOfficial rewrites the OpenStreetMap results in merge so each trail park
// with an official route of a matching name near its pin takes that route
// instead. The national historic trails are mostly old wagon roads and water
// routes that OpenStreetMap doesn't map as one trail.
func useOfficial(parks []*park, officialPath, merge, out string) error {
	routes, err := readOfficial(officialPath)
	if err != nil {
		return err
	}
	names := make([]string, 0, len(routes))
	for n := range routes {
		names = append(names, n)
	}
	sort.Strings(names)
	osmLines := map[string][]byte{}
	if merge != "" {
		err := eachLine(merge, func(b []byte) error {
			var f struct{ Properties struct{ Ref string } }
			if json.Unmarshal(b, &f) == nil {
				osmLines[f.Properties.Ref] = append([]byte(nil), b...)
			}
			return nil
		})
		if err != nil {
			return err
		}
	}

	f, err := os.Create(out)
	if err != nil {
		return err
	}
	defer f.Close()
	gz := gzip.NewWriter(f)
	w := bufio.NewWriter(gz)
	used := 0
	for _, p := range parks {
		var best *official
		var bestLines orb.MultiLineString
		bestScore, bestKm := 0.0, 0.0
		// A byway or river's pin is on its route, and their official names
		// are shorter ("Payette River Scenic Byway").
		cands, minScore, maxKm := names, officialScore, float64(officialKm)
		along := match.IsByway(p.Name) || match.IsWildRiver(p.Name)
		if along {
			minScore, maxKm = alongScore, alongKm
		} else if !match.EndsInTrail(p.Name) {
			cands = nil // keeps its OpenStreetMap route
		}
		for _, n := range cands {
			if !match.RouteFits(p.Name, n) || !match.SharesName(p.Name, n) || match.Conflicts(p.Name, n) {
				continue
			}
			// Only the trail itself takes the route, not a park named
			// after it ("Trail of Tears State Forest"). Its pin can be well
			// off a route across several states.
			s := match.Similarity(p.Name, n)
			if s < minScore || s < bestScore {
				continue
			}
			lines := routes[n].lines
			if along {
				lines = routes[n].near(p.Pt, maxKm)
			}
			km := nearestKm(lines, p.Pt)
			if len(lines) == 0 || km > maxKm || s == bestScore && km >= bestKm {
				continue
			}
			best, bestLines, bestScore, bestKm = routes[n], lines, s, km
		}
		if best == nil {
			if b := osmLines[p.Ref]; b != nil {
				w.Write(b)
				w.WriteString("\n")
			}
			continue
		}
		ft := geojson.NewFeature(bestLines)
		ft.Properties["ref"] = p.Ref
		ft.Properties["osm"] = "official/" + best.source
		ft.Properties["osm_name"] = best.name
		ft.Properties["manager"] = best.manager
		ft.Properties["score"] = bestScore
		b, err := json.Marshal(ft)
		if err != nil {
			return err
		}
		w.Write(b)
		w.WriteString("\n")
		used++
		km := 0.0
		for _, l := range bestLines {
			km += lengthKm(l)
		}
		was := "no OpenStreetMap route"
		if b := osmLines[p.Ref]; b != nil && !strings.Contains(string(b), `"geometry":null`) {
			was = "replaces the OpenStreetMap route"
		}
		fmt.Printf("%s %s (%s) | official %q from %s %.2f, %.0f km, %s\n", p.Ref, p.Name, p.Loc, best.name, best.source, bestScore, km, was)
	}
	fmt.Printf("\n== Official routes: %d trail, byway and river parks\n", used)
	if err := w.Flush(); err != nil {
		return err
	}
	return gz.Close()
}

// eachLine calls fn with each line of a gzipped file.
func eachLine(path string, fn func([]byte) error) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	gz, err := gzip.NewReader(f)
	if err != nil {
		return err
	}
	defer gz.Close()
	sc := bufio.NewScanner(gz)
	sc.Buffer(make([]byte, 1<<20), 1<<30)
	for sc.Scan() {
		if len(sc.Bytes()) == 0 {
			continue
		}
		if err := fn(sc.Bytes()); err != nil {
			return err
		}
	}
	return sc.Err()
}
