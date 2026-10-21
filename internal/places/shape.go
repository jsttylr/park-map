package places

import (
	"math"

	"github.com/paulmach/orb"
)

// Shape is a multipolygon with bounding boxes kept for every polygon and ring,
// and long rings cut into horizontal strips, so point tests only look at the
// few edges near the point.
type Shape struct {
	MP    orb.MultiPolygon
	Bound orb.Bound
	rings [][]*ring // per polygon: outer ring first, then holes
}

type ring struct {
	pts    orb.Ring
	bound  orb.Bound
	strips [][]int32 // edge start indexes by y strip; nil for short rings
	y0, dy float64
}

const stripEdges = 8 // aim for about this many edges per strip

func newShape(mp orb.MultiPolygon) *Shape {
	s := &Shape{MP: mp, Bound: mp.Bound()}
	s.rings = make([][]*ring, len(mp))
	for i, poly := range mp {
		s.rings[i] = make([]*ring, len(poly))
		for j, r := range poly {
			s.rings[i][j] = newRing(r)
		}
	}
	return s
}

func newRing(pts orb.Ring) *ring {
	r := &ring{pts: pts, bound: pts.Bound()}
	n := len(pts)
	if n < 64 {
		return r
	}
	ns := min(n/stripEdges, 4096)
	r.y0 = r.bound.Min[1]
	r.dy = (r.bound.Max[1] - r.bound.Min[1]) / float64(ns)
	if r.dy <= 0 {
		return r
	}
	r.strips = make([][]int32, ns)
	for k := 0; k < n; k++ {
		a, b := pts[k], pts[(k+1)%n]
		lo, hi := r.strip(math.Min(a[1], b[1])), r.strip(math.Max(a[1], b[1]))
		for x := lo; x <= hi; x++ {
			r.strips[x] = append(r.strips[x], int32(k))
		}
	}
	return r
}

func (r *ring) strip(y float64) int {
	return max(0, min(len(r.strips)-1, int((y-r.y0)/r.dy)))
}

// edges calls fn for every edge that may reach the y range lo..hi.
func (r *ring) edges(lo, hi float64, fn func(a, b orb.Point)) {
	n := len(r.pts)
	if r.strips == nil {
		for k := 0; k < n; k++ {
			fn(r.pts[k], r.pts[(k+1)%n])
		}
		return
	}
	for x := r.strip(lo); x <= r.strip(hi); x++ {
		for _, k := range r.strips[x] {
			fn(r.pts[k], r.pts[(int(k)+1)%n])
		}
	}
}

func (r *ring) contains(p orb.Point) bool {
	if !r.bound.Contains(p) {
		return false
	}
	in := false
	r.edges(p[1], p[1], func(a, b orb.Point) {
		if (a[1] > p[1]) != (b[1] > p[1]) && p[0] < (b[0]-a[0])*(p[1]-a[1])/(b[1]-a[1])+a[0] {
			in = !in
		}
	})
	return in
}

// Contains reports whether p is inside the shape.
func (s *Shape) Contains(p orb.Point) bool {
	if !s.Bound.Contains(p) {
		return false
	}
	for _, poly := range s.rings {
		if len(poly) == 0 || !poly[0].contains(p) {
			continue
		}
		hole := false
		for _, h := range poly[1:] {
			if h.contains(p) {
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
	kx := math.Cos(p[1] * math.Pi / 180)
	near := orb.Bound{Min: p, Max: p}.Pad(margin / math.Max(kx, 0.1))
	m2 := margin * margin
	ok := true
	for _, poly := range s.rings {
		for _, r := range poly {
			if !ok {
				return false
			}
			if !r.bound.Intersects(near) {
				continue
			}
			r.edges(near.Min[1], near.Max[1], func(a, b orb.Point) {
				if ok && segDist2(a, b, p, kx) < m2 {
					ok = false
				}
			})
		}
	}
	return ok
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
