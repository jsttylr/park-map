// Package places holds POTA parks and SOTA summits with their outlines, finds
// what is at a point, and works out where they overlap (the n-fers).
package places

import (
	"bufio"
	"compress/gzip"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"math"
	"os"
	"sort"
	"strings"

	"github.com/paulmach/orb"
	"github.com/paulmach/orb/geojson"
)

type Kind uint8

const (
	Park Kind = iota
	Summit
)

func (k Kind) String() string {
	if k == Summit {
		return "summit"
	}
	return "park"
}

// Place is one park or summit. Shape is nil when only its point is known.
type Place struct {
	ID    int
	Kind  Kind
	Ref   string
	Name  string
	Loc   string // park: state(s); summit: region
	Pt    orb.Point
	Shape *Shape
	Props geojson.Properties

	// NFer is the most parks you can be in at once somewhere in this place;
	// NFerAll also counts summit activation zones. Both are at least 1.
	NFer, NFerAll int
	// Overlaps lists the IDs of places whose outline overlaps this one.
	Overlaps []int

	lod []orb.MultiPolygon // simplified copies for low zoom tiles
}

// Set is every loaded place with a grid index over their bounding boxes.
type Set struct {
	Places []*Place
	grid   map[cellKey][]int32
}

const cellDeg = 0.25

type cellKey struct{ x, y int32 }

func cellOf(x, y float64) cellKey {
	return cellKey{int32(math.Floor(x / cellDeg)), int32(math.Floor(y / cellDeg))}
}

func (p *Place) Bound() orb.Bound {
	if p.Shape != nil {
		return p.Shape.Bound
	}
	return p.Pt.Bound()
}

// Load reads parks and summits from gzipped GeoJSON files. Either path may be
// empty; a missing summits file is logged and skipped.
func Load(parksPath, summitsPath string) (*Set, error) {
	s := &Set{grid: map[cellKey][]int32{}}
	if parksPath != "" {
		if err := s.load(parksPath, Park); err != nil {
			return nil, err
		}
	}
	if summitsPath != "" {
		if err := s.load(summitsPath, Summit); err != nil {
			if os.IsNotExist(err) {
				log.Printf("no summits: %v", err)
			} else {
				return nil, err
			}
		}
	}
	return s, nil
}

func (s *Set) load(path string, kind Kind) error {
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
		r = gz
	}
	// Read one feature at a time so the whole file never sits in memory.
	dec := json.NewDecoder(bufio.NewReaderSize(r, 1<<20))
	for {
		tok, err := dec.Token()
		if err != nil {
			return fmt.Errorf("%s: no features array: %v", path, err)
		}
		if tok == "features" {
			break
		}
	}
	if _, err := dec.Token(); err != nil { // [
		return err
	}
	for dec.More() {
		var ft geojson.Feature
		if err := dec.Decode(&ft); err != nil {
			return fmt.Errorf("%s: %v", path, err)
		}
		s.Add(fromFeature(&ft, kind))
	}
	return nil
}

func fromFeature(ft *geojson.Feature, kind Kind) *Place {
	str := func(k string) string { v, _ := ft.Properties[k].(string); return v }
	num := func(k string) float64 { v, _ := ft.Properties[k].(float64); return v }
	p := &Place{Kind: kind, Ref: str("ref"), Name: str("name"), Props: ft.Properties, NFer: 1, NFerAll: 1}
	p.Loc = str("loc")
	if kind == Summit {
		p.Loc = str("region")
	}
	p.Pt = orb.Point{num("lon"), num("lat")}
	switch g := ft.Geometry.(type) {
	case orb.Point:
		if p.Pt == (orb.Point{}) {
			p.Pt = g
		}
	case orb.Polygon:
		p.Shape = newShape(orb.MultiPolygon{g})
	case orb.MultiPolygon:
		p.Shape = newShape(g)
	}
	if p.Shape != nil && len(p.Shape.MP) == 0 {
		p.Shape = nil
	}
	return p
}

// Add puts a place in the set and the index.
func (s *Set) Add(p *Place) {
	if s.grid == nil {
		s.grid = map[cellKey][]int32{}
	}
	if p.Pt == (orb.Point{}) && p.Shape != nil {
		p.Pt = p.Shape.Bound.Center()
	}
	if p.NFer == 0 {
		p.NFer, p.NFerAll = 1, 1
	}
	p.ID = len(s.Places)
	s.Places = append(s.Places, p)
	b := p.Bound()
	lo, hi := cellOf(b.Min[0], b.Min[1]), cellOf(b.Max[0], b.Max[1])
	for x := lo.x; x <= hi.x; x++ {
		for y := lo.y; y <= hi.y; y++ {
			k := cellKey{x, y}
			s.grid[k] = append(s.grid[k], int32(p.ID))
		}
	}
}

// Within returns the places whose bounding box meets b.
func (s *Set) Within(b orb.Bound) []*Place {
	lo, hi := cellOf(b.Min[0], b.Min[1]), cellOf(b.Max[0], b.Max[1])
	var out []*Place
	seen := map[int32]bool{}
	for x := lo.x; x <= hi.x; x++ {
		for y := lo.y; y <= hi.y; y++ {
			for _, id := range s.grid[cellKey{x, y}] {
				if seen[id] {
					continue
				}
				seen[id] = true
				if p := s.Places[id]; p.Bound().Intersects(b) {
					out = append(out, p)
				}
			}
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

// Near is a place close to a point, with the distance in km.
type Near struct {
	*Place
	Km float64
}

// At returns the places whose outline contains pt, and the places with only a
// point (no outline) within nearKm, closest first.
func (s *Set) At(pt orb.Point, nearKm float64) (in []*Place, near []Near) {
	pad := nearKm / 111
	b := orb.Bound{Min: pt, Max: pt}.Pad(pad / math.Max(math.Cos(pt[1]*math.Pi/180), 0.1))
	for _, p := range s.Within(b) {
		if p.Shape != nil {
			if p.Shape.Contains(pt) {
				in = append(in, p)
			}
			continue
		}
		if d := Km(pt, p.Pt); d <= nearKm {
			near = append(near, Near{p, d})
		}
	}
	sort.SliceStable(in, func(i, j int) bool { return in[i].Kind < in[j].Kind })
	sort.Slice(near, func(i, j int) bool { return near[i].Km < near[j].Km })
	return in, near
}

// Km is the great-circle distance between two points.
func Km(a, b orb.Point) float64 {
	const r = 6371.0
	la1, la2 := a[1]*math.Pi/180, b[1]*math.Pi/180
	dla, dlo := la2-la1, (b[0]-a[0])*math.Pi/180
	h := math.Sin(dla/2)*math.Sin(dla/2) + math.Cos(la1)*math.Cos(la2)*math.Sin(dlo/2)*math.Sin(dlo/2)
	return 2 * r * math.Asin(math.Min(1, math.Sqrt(h)))
}

// Search finds places whose reference or name contains q, references first.
func (s *Set) Search(q string, limit int) []*Place {
	q = strings.ToLower(strings.TrimSpace(q))
	if q == "" {
		return nil
	}
	var exact, ref, name []*Place
	for _, p := range s.Places {
		r := strings.ToLower(p.Ref)
		switch {
		case r == q:
			exact = append(exact, p)
		case strings.HasPrefix(r, q):
			ref = append(ref, p)
		case strings.Contains(strings.ToLower(p.Name), q):
			name = append(name, p)
		}
	}
	out := append(append(exact, ref...), name...)
	if len(out) > limit {
		out = out[:limit]
	}
	return out
}
