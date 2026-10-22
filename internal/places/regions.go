package places

import (
	"bufio"
	"compress/gzip"
	"encoding/json"
	"fmt"
	"log"
	"math"
	"os"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/engelsjk/polygol"
	"github.com/paulmach/orb"
	"github.com/paulmach/orb/clip"
	"github.com/paulmach/orb/geojson"
	"github.com/paulmach/orb/planar"
)

// Region is the Kind of a place that is an n-fer area: the exact patch where
// two or more outlines overlap. Its NFer is the number of parks there, NFerAll
// the parks plus summit activation zones.
const Region Kind = 2

// Smallest overlap worth showing: at least this many square meters, and at
// least this thick (twice the area over the perimeter), so slivers along
// shared boundaries are dropped.
const (
	minRegionM2    = 2000
	minRegionThick = 15
)

// ComputeRegions works out the overlap patches for every group of places
// that overlap (ComputeOverlaps must run first) and keeps them in s.Regions.
func (s *Set) ComputeRegions() {
	t := time.Now()
	var mu sync.Mutex
	var out []*Place
	var done atomic.Int64
	parallel(len(s.Places), func(i int) {
		if n := done.Add(1); n%10000 == 0 {
			log.Printf("regions: %d of %d places", n, len(s.Places))
		}
		a := s.Places[i]
		var mine []*Place
		emit := func(members []*Place, mp orb.MultiPolygon) {
			mp = s.only(members, mp)
			if len(mp) == 0 {
				return
			}
			r := &Place{Kind: Region, Shape: newShape(mp)}
			refs := make([]string, len(members))
			for k, m := range members {
				refs[k] = m.Ref
				r.NFerAll++
				if m.Kind == Park {
					r.NFer++
				}
			}
			r.Ref = strings.Join(refs, " ")
			if len(r.Shape.MP) > 0 {
				mine = append(mine, r)
			}
		}
		var grow func(members []*Place, area orb.MultiPolygon, cands []int)
		grow = func(members []*Place, area orb.MultiPolygon, cands []int) {
			last := members[len(members)-1]
			for _, c := range cands {
				if c <= last.ID {
					continue
				}
				p := s.Places[c]
				next := intersect(area, p.Shape)
				if len(next) == 0 {
					continue
				}
				m := append(append([]*Place{}, members...), p)
				emit(m, next)
				grow(m, next, common(cands, p.Overlaps))
			}
		}
		for _, j := range a.Overlaps {
			if j <= a.ID {
				continue
			}
			b := s.Places[j]
			area := intersect(a.Shape.MP, b.Shape)
			if len(area) == 0 {
				continue
			}
			emit([]*Place{a, b}, area)
			grow([]*Place{a, b}, area, common(a.Overlaps, b.Overlaps))
		}
		if len(mine) > 0 {
			mu.Lock()
			out = append(out, mine...)
			mu.Unlock()
		}
	})
	// Fewer overlaps first, so busier patches draw on top.
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].NFerAll != out[j].NFerAll {
			return out[i].NFerAll < out[j].NFerAll
		}
		return out[i].Ref < out[j].Ref
	})
	s.Regions = &Set{}
	for _, r := range out {
		s.Regions.Add(r)
	}
	s.nferFromRegions()
	log.Printf("regions: %d overlap patches in %v", len(out), time.Since(t).Round(time.Millisecond))
}

// only trims an overlap patch to where exactly these members overlap, by
// cutting away every other place that covers part of it. Patches then never
// stack: a spot inside three parks is painted once, by the 3-fer patch, not
// also by the three 2-fer patches beneath it, so its color matches the legend
// whatever else overlaps nearby.
func (s *Set) only(members []*Place, area orb.MultiPolygon) (out orb.MultiPolygon) {
	others := members[0].Overlaps
	for _, m := range members[1:] {
		others = common(others, m.Overlaps)
	}
	b := area.Bound().Pad(1e-6)
	var cut []polygol.Geom
	for _, id := range others {
		p := s.Places[id]
		if !p.Shape.Bound.Intersects(b) {
			continue
		}
		if y := clipTo(p.Shape.MP, b); len(y) > 0 {
			cut = append(cut, toGeom(y))
		}
	}
	if len(cut) == 0 {
		return area
	}
	defer func() {
		if r := recover(); r != nil {
			out = area // the clipping library gave up on odd geometry; keep the whole patch
		}
	}()
	g, err := polygol.Difference(toGeom(area), cut...)
	if err != nil {
		return area
	}
	return fromGeom(g)
}

// common returns the sorted IDs found in both sorted lists.
func common(a, b []int) []int {
	var out []int
	for i, j := 0, 0; i < len(a) && j < len(b); {
		switch {
		case a[i] == b[j]:
			out = append(out, a[i])
			i++
			j++
		case a[i] < b[j]:
			i++
		default:
			j++
		}
	}
	return out
}

// intersect returns the part of area inside shape, without slivers. Both are
// cut to their shared bounding box first so huge outlines stay quick.
func intersect(area orb.MultiPolygon, shape *Shape) (out orb.MultiPolygon) {
	ab := area.Bound()
	b := orb.Bound{
		Min: orb.Point{math.Max(ab.Min[0], shape.Bound.Min[0]), math.Max(ab.Min[1], shape.Bound.Min[1])},
		Max: orb.Point{math.Min(ab.Max[0], shape.Bound.Max[0]), math.Min(ab.Max[1], shape.Bound.Max[1])},
	}
	if b.Min[0] >= b.Max[0] || b.Min[1] >= b.Max[1] {
		return nil
	}
	b = b.Pad(1e-6)
	x := clipTo(area, b)
	y := clipTo(shape.MP, b)
	if len(x) == 0 || len(y) == 0 {
		return nil
	}
	defer func() {
		if r := recover(); r != nil {
			out = nil // the clipping library gave up on odd geometry; skip it
		}
	}()
	g, err := polygol.Intersection(toGeom(x), toGeom(y))
	if err != nil {
		return nil
	}
	return fromGeom(g)
}

// fromGeom converts a clipping result back, without slivers.
func fromGeom(g polygol.Geom) (out orb.MultiPolygon) {
	for _, poly := range g {
		p := make(orb.Polygon, len(poly))
		for i, ring := range poly {
			r := make(orb.Ring, len(ring))
			for k, pt := range ring {
				r[k] = orb.Point{pt[0], pt[1]}
			}
			p[i] = r
		}
		if big(p) {
			out = append(out, p)
		}
	}
	return clean(out)
}

func clipTo(mp orb.MultiPolygon, b orb.Bound) orb.MultiPolygon {
	var parts orb.MultiPolygon
	for _, p := range mp {
		if len(p) > 0 && p[0].Bound().Intersects(b) {
			parts = append(parts, p)
		}
	}
	if len(parts) == 0 {
		return nil
	}
	return clean(clip.MultiPolygon(b, orb.Clone(parts).(orb.MultiPolygon)))
}

func toGeom(mp orb.MultiPolygon) polygol.Geom {
	g := make(polygol.Geom, len(mp))
	for i, poly := range mp {
		g[i] = make([][][]float64, len(poly))
		for j, ring := range poly {
			g[i][j] = make([][]float64, len(ring))
			for k, pt := range ring {
				g[i][j][k] = []float64{pt[0], pt[1]}
			}
		}
	}
	return g
}

// big reports whether a polygon has enough area and thickness to be a real
// overlap rather than a sliver along a shared edge.
func big(p orb.Polygon) bool {
	if len(p) == 0 || len(p[0]) < 4 {
		return false
	}
	lat := p[0].Bound().Center()[1]
	kx := math.Cos(lat*math.Pi/180) * 111320
	const ky = 110540
	area, perim := 0.0, 0.0
	for i, r := range p {
		a := 0.0
		for k := 0; k+1 < len(r); k++ {
			x0, y0 := r[k][0]*kx, r[k][1]*ky
			x1, y1 := r[k+1][0]*kx, r[k+1][1]*ky
			a += x0*y1 - x1*y0
			perim += math.Hypot(x1-x0, y1-y0)
		}
		a = math.Abs(a) / 2
		if i == 0 {
			area += a
		} else {
			area -= a
		}
	}
	return area >= minRegionM2 && perim > 0 && 2*area/perim >= minRegionThick
}

// WriteRegions saves s.Regions as gzipped GeoJSON for LoadRegions.
func (s *Set) WriteRegions(path string) error {
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()
	gz := gzip.NewWriter(f)
	w := bufio.NewWriter(gz)
	w.WriteString(`{"type":"FeatureCollection","features":[`)
	for i, r := range s.Regions.Places {
		ft := geojson.NewFeature(orb.Round(r.Shape.MP, 1e5))
		ft.Properties = geojson.Properties{"ref": r.Ref, "parks": r.NFer, "all": r.NFerAll}
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

// LoadRegions reads overlap patches saved by WriteRegions into s.Regions.
func (s *Set) LoadRegions(path string) error {
	r := &Set{}
	if err := r.load(path, Region); err != nil {
		return err
	}
	for _, p := range r.Places {
		p.NFer, _ = toInt(p.Props["parks"])
		p.NFerAll, _ = toInt(p.Props["all"])
		p.Props = nil
	}
	s.Regions = r
	s.nferFromRegions()
	return nil
}

// nferFromRegions raises each place's n-fer count to the busiest patch it is
// part of. ComputeOverlaps counts from test points, which can miss a third
// place along a trail only 200 ft wide; the patches are cut exactly.
func (s *Set) nferFromRegions() {
	byRef := map[string]*Place{}
	for _, p := range s.Places {
		byRef[p.Ref] = p
	}
	for _, r := range s.Regions.Places {
		for _, ref := range strings.Fields(r.Ref) {
			if p := byRef[ref]; p != nil {
				p.NFer = max(p.NFer, r.NFer)
				p.NFerAll = max(p.NFerAll, r.NFerAll)
			}
		}
	}
}

func toInt(v any) (int, error) {
	if f, ok := v.(float64); ok {
		return int(f), nil
	}
	return 0, fmt.Errorf("not a number: %v", v)
}

// inner returns a point inside the patch for drawing it as a dot: the middle
// of its bounding box if that's inside, otherwise a point near the middle of
// its biggest piece.
func (p *Place) inner() orb.Point {
	if p.Shape == nil || p.Shape.Contains(p.Pt) {
		return p.Pt
	}
	best, area := orb.Polygon(nil), -1.0
	for _, poly := range p.Shape.MP {
		if a := math.Abs(planar.Area(poly)); a > area {
			best, area = poly, a
		}
	}
	if c, _ := planar.CentroidArea(best); p.Shape.Contains(c) {
		return c
	}
	// Scan across the middle of the piece for the widest stretch inside it.
	b := best.Bound()
	y := (b.Min[1] + b.Max[1]) / 2
	for i := 1; i < 64; i++ {
		pt := orb.Point{b.Min[0] + (b.Max[0]-b.Min[0])*float64(i)/64, y}
		if p.Shape.Contains(pt) {
			return pt
		}
	}
	return best[0][0]
}
