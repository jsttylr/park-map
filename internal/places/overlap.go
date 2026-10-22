package places

import (
	"log"
	"math"
	"runtime"
	"sort"
	"sync"
	"sync/atomic"
	"time"

	"github.com/paulmach/orb"
)

const (
	// edgeMargin keeps parks that only share a boundary from counting as
	// overlapping: a spot must be this far inside both (about 25 m).
	edgeMargin = 0.000225
	// samples per side of the grid of test points laid over the area where two
	// outlines' bounding boxes meet.
	samples = 12
)

type pair struct {
	a, b int
	pts  []orb.Point // test points inside both
}

// ComputeOverlaps finds which outlines overlap and the n-fer count for every
// place: the most places you can stand in at once somewhere inside it. It
// samples points first and cuts the outlines against each other only when
// the samples find nothing.
func (s *Set) ComputeOverlaps() {
	t := time.Now()
	pairs := s.findPairs()
	log.Printf("overlaps: %d overlapping pairs found in %v", len(pairs), time.Since(t).Round(time.Millisecond))
	defer func() { log.Printf("overlaps: n-fer counts done in %v", time.Since(t).Round(time.Millisecond)) }()
	nb := make([]map[int]bool, len(s.Places))
	for _, pr := range pairs {
		for _, x := range [][2]int{{pr.a, pr.b}, {pr.b, pr.a}} {
			if nb[x[0]] == nil {
				nb[x[0]] = map[int]bool{}
			}
			nb[x[0]][x[1]] = true
		}
	}
	for i, m := range nb {
		p := s.Places[i]
		p.Overlaps = p.Overlaps[:0]
		for j := range m {
			p.Overlaps = append(p.Overlaps, j)
		}
		sort.Ints(p.Overlaps)
	}

	type best struct{ parks, all int }
	var mu sync.Mutex
	parallel(len(pairs), func(k int) {
		pr := pairs[k]
		var common []*Place
		for j := range nb[pr.a] {
			if j != pr.b && nb[pr.b][j] {
				common = append(common, s.Places[j])
			}
		}
		local := map[int]best{}
		for _, pt := range pr.pts {
			members := []*Place{s.Places[pr.a], s.Places[pr.b]}
			for _, c := range common {
				if c.Shape.Inside(pt, edgeMargin) {
					members = append(members, c)
				}
			}
			parks := 0
			for _, m := range members {
				if m.Kind == Park {
					parks++
				}
			}
			for _, m := range members {
				b := local[m.ID]
				local[m.ID] = best{max(b.parks, parks), max(b.all, len(members))}
			}
		}
		mu.Lock()
		for id, b := range local {
			p := s.Places[id]
			p.NFer = max(p.NFer, b.parks)
			p.NFerAll = max(p.NFerAll, b.all)
		}
		mu.Unlock()
	})
}

func (s *Set) findPairs() []pair {
	var mu sync.Mutex
	var pairs []pair
	var done, exact atomic.Int64
	defer func() { log.Printf("overlaps: %d pairs checked exactly", exact.Load()) }()
	parallel(len(s.Places), func(i int) {
		if n := done.Add(1); n%5000 == 0 {
			log.Printf("overlaps: checked %d of %d places", n, len(s.Places))
		}
		a := s.Places[i]
		if a.Shape == nil {
			return
		}
		start := time.Now()
		var mine []pair
		cands := s.Within(a.Shape.Bound)
		defer func() {
			if d := time.Since(start); d > 2*time.Second {
				verts := 0
				for _, poly := range a.Shape.MP {
					for _, r := range poly {
						verts += len(r)
					}
				}
				log.Printf("overlaps: slow %s %s %q: %v, %d parts, %d points, %d nearby, %d overlaps",
					a.Kind, a.Ref, a.Name, d.Round(time.Millisecond), len(a.Shape.MP), verts, len(cands), len(mine))
			}
		}()
		for _, b := range cands {
			if b.ID <= a.ID || b.Shape == nil {
				continue
			}
			ab, bb := a.Shape.Bound, b.Shape.Bound
			ib := orb.Bound{
				Min: orb.Point{math.Max(ab.Min[0], bb.Min[0]), math.Max(ab.Min[1], bb.Min[1])},
				Max: orb.Point{math.Min(ab.Max[0], bb.Max[0]), math.Min(ab.Max[1], bb.Max[1])},
			}
			kx := math.Cos(ib.Center()[1] * math.Pi / 180)
			if ib.Max[1]-ib.Min[1] < 2*edgeMargin || (ib.Max[0]-ib.Min[0])*kx < 2*edgeMargin {
				continue
			}
			var pts []orb.Point
			for x := 0; x < samples; x++ {
				for y := 0; y < samples; y++ {
					pt := orb.Point{
						ib.Min[0] + (float64(x)+0.5)/samples*(ib.Max[0]-ib.Min[0]),
						ib.Min[1] + (float64(y)+0.5)/samples*(ib.Max[1]-ib.Min[1]),
					}
					if a.Shape.Inside(pt, edgeMargin) && b.Shape.Inside(pt, edgeMargin) {
						pts = append(pts, pt)
					}
				}
			}
			// The grid can step right over a thin outline, like a trail
			// corridor a few hundred meters wide crossing a forest, so when
			// it finds nothing the outlines are cut against each other.
			if len(pts) == 0 {
				exact.Add(1)
				for _, poly := range intersect(a.Shape.MP, b.Shape) {
					piece := &Place{Shape: newShape(orb.MultiPolygon{poly})}
					pts = append(pts, piece.inner())
				}
			}
			if len(pts) > 0 {
				mine = append(mine, pair{a.ID, b.ID, pts})
			}
		}
		if len(mine) > 0 {
			mu.Lock()
			pairs = append(pairs, mine...)
			mu.Unlock()
		}
	})
	return pairs
}

func parallel(n int, fn func(int)) {
	var wg sync.WaitGroup
	next := make(chan int)
	for w := 0; w < runtime.NumCPU(); w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := range next {
				fn(i)
			}
		}()
	}
	for i := 0; i < n; i++ {
		next <- i
	}
	close(next)
	wg.Wait()
}
