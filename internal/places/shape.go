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

	// For shapes made of many parcels, which polygons reach each grid cell.
	cells        [][]int32
	nx, ny       int
	cellW, cellH float64
}

const gridSide = 64 // grid cells per side for shapes with many polygons

type ring struct {
	pts    orb.Ring
	bound  orb.Bound
	strips [][]int32 // edge start indexes by y strip; nil for short rings
	y0, dy float64
}

const stripEdges = 8 // aim for about this many edges per strip

// clean drops empty polygons and rings too short to enclose anything.
func clean(mp orb.MultiPolygon) orb.MultiPolygon {
	out := mp[:0]
	for _, poly := range mp {
		var rings orb.Polygon
		for i, r := range poly {
			if len(r) >= 4 {
				rings = append(rings, r)
			} else if i == 0 {
				break
			}
		}
		if len(rings) > 0 && len(poly) > 0 && len(poly[0]) >= 4 {
			out = append(out, rings)
		}
	}
	return out
}

func newShape(mp orb.MultiPolygon) *Shape {
	mp = clean(mp)
	s := &Shape{MP: mp, Bound: mp.Bound()}
	s.rings = make([][]*ring, len(mp))
	for i, poly := range mp {
		s.rings[i] = make([]*ring, len(poly))
		for j, r := range poly {
			s.rings[i][j] = newRing(r)
		}
	}
	if len(mp) > 32 {
		s.nx, s.ny = gridSide, gridSide
		s.cellW = (s.Bound.Max[0] - s.Bound.Min[0]) / gridSide
		s.cellH = (s.Bound.Max[1] - s.Bound.Min[1]) / gridSide
		if s.cellW > 0 && s.cellH > 0 {
			s.cells = make([][]int32, gridSide*gridSide)
			for i, poly := range s.rings {
				if len(poly) == 0 {
					continue
				}
				b := poly[0].bound
				x0, y0 := s.cell(b.Min)
				x1, y1 := s.cell(b.Max)
				for x := x0; x <= x1; x++ {
					for y := y0; y <= y1; y++ {
						s.cells[y*gridSide+x] = append(s.cells[y*gridSide+x], int32(i))
					}
				}
			}
		}
	}
	return s
}

func (s *Shape) cell(p orb.Point) (int, int) {
	x := int((p[0] - s.Bound.Min[0]) / s.cellW)
	y := int((p[1] - s.Bound.Min[1]) / s.cellH)
	return max(0, min(s.nx-1, x)), max(0, min(s.ny-1, y))
}

// polys calls fn with the index of every polygon that may reach b, until fn
// returns false.
func (s *Shape) polys(b orb.Bound, fn func(i int) bool) {
	if s.cells == nil {
		for i := range s.rings {
			if !fn(i) {
				return
			}
		}
		return
	}
	x0, y0 := s.cell(b.Min)
	x1, y1 := s.cell(b.Max)
	if x0 == x1 && y0 == y1 {
		for _, i := range s.cells[y0*s.nx+x0] {
			if !fn(int(i)) {
				return
			}
		}
		return
	}
	seen := map[int32]bool{}
	for x := x0; x <= x1; x++ {
		for y := y0; y <= y1; y++ {
			for _, i := range s.cells[y*s.nx+x] {
				if !seen[i] {
					seen[i] = true
					if !fn(int(i)) {
						return
					}
				}
			}
		}
	}
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
	found := false
	s.polys(orb.Bound{Min: p, Max: p}, func(i int) bool {
		poly := s.rings[i]
		if len(poly) == 0 || !poly[0].contains(p) {
			return true
		}
		for _, h := range poly[1:] {
			if h.contains(p) {
				return true
			}
		}
		found = true
		return false
	})
	return found
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
	s.polys(near, func(i int) bool {
		for _, r := range s.rings[i] {
			if !r.bound.Intersects(near) {
				continue
			}
			r.edges(near.Min[1], near.Max[1], func(a, b orb.Point) {
				if ok && segDist2(a, b, p, kx) < m2 {
					ok = false
				}
			})
		}
		return ok
	})
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

// NewShape prepares a multipolygon for point tests.
func NewShape(mp orb.MultiPolygon) *Shape { return newShape(mp) }
