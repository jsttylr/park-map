package places

import (
	"math"

	"github.com/paulmach/orb"
)

// Shape is a multipolygon with bounding boxes kept for every polygon and ring,
// so point tests skip the parts that are nowhere near.
type Shape struct {
	MP     orb.MultiPolygon
	Bound  orb.Bound
	bounds [][]orb.Bound // per polygon, per ring
}

func newShape(mp orb.MultiPolygon) *Shape {
	s := &Shape{MP: mp, Bound: mp.Bound()}
	s.bounds = make([][]orb.Bound, len(mp))
	for i, poly := range mp {
		s.bounds[i] = make([]orb.Bound, len(poly))
		for j, ring := range poly {
			s.bounds[i][j] = ring.Bound()
		}
	}
	return s
}

// Contains reports whether p is inside the shape (on the edge counts).
func (s *Shape) Contains(p orb.Point) bool {
	if !s.Bound.Contains(p) {
		return false
	}
	for i, poly := range s.MP {
		if len(poly) == 0 || !s.bounds[i][0].Contains(p) || !ringContains(poly[0], p) {
			continue
		}
		hole := false
		for j := 1; j < len(poly); j++ {
			if s.bounds[i][j].Contains(p) && ringContains(poly[j], p) {
				hole = true
				break
			}
		}
		if !hole {
			return true
		}
	}
	return false
}

// Inside reports whether p is inside the shape and at least margin degrees of
// latitude (scaled for longitude) from its edge. It keeps a shared boundary
// between neighbouring parks from counting as an overlap.
func (s *Shape) Inside(p orb.Point, margin float64) bool {
	if !s.Contains(p) {
		return false
	}
	near := orb.Bound{Min: p, Max: p}.Pad(margin / math.Max(math.Cos(p[1]*math.Pi/180), 0.1))
	kx := math.Cos(p[1] * math.Pi / 180)
	m2 := margin * margin
	for i, poly := range s.MP {
		for j, ring := range poly {
			if !s.bounds[i][j].Intersects(near) {
				continue
			}
			for k := 0; k+1 < len(ring); k++ {
				if segDist2(ring[k], ring[k+1], p, kx) < m2 {
					return false
				}
			}
		}
	}
	return true
}

func ringContains(r orb.Ring, p orb.Point) bool {
	in := false
	n := len(r)
	for i, j := 0, n-1; i < n; j, i = i, i+1 {
		a, b := r[i], r[j]
		if (a[1] > p[1]) != (b[1] > p[1]) &&
			p[0] < (b[0]-a[0])*(p[1]-a[1])/(b[1]-a[1])+a[0] {
			in = !in
		}
	}
	return in
}

// segDist2 is the squared distance from p to segment ab, with longitudes
// scaled by kx so both axes are in degrees of latitude.
func segDist2(a, b, p orb.Point, kx float64) float64 {
	ax, ay := (a[0]-p[0])*kx, a[1]-p[1]
	bx, by := (b[0]-p[0])*kx, b[1]-p[1]
	dx, dy := bx-ax, by-ay
	t := 0.0
	if l := dx*dx + dy*dy; l > 0 {
		t = math.Max(0, math.Min(1, -(ax*dx+ay*dy)/l))
	}
	x, y := ax+t*dx, ay+t*dy
	return x*x + y*y
}
