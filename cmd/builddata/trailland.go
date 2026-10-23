package main

import (
	"fmt"
	"math"
	"sort"

	"github.com/engelsjk/polygol"
	"github.com/paulmach/orb"
	"github.com/paulmach/orb/clip"
	"github.com/paulmach/orb/planar"
)

const (
	landCell    = 0.1 // grid the land and band are compared on, degrees
	acresPerSqM = 1 / 4046.86
)

// landVsBand compares a trail park's PAD-US land with the band 100 ft either
// side of its route, cell by cell so huge outlines stay quick. It returns
// the acres of each, how much of each lies outside the other, and the two
// put together.
type landVsBand struct {
	p                                  *park
	unit, manager, desig, layer        string
	land, band, landBeyond, bandBeyond float64
	union                              orb.MultiPolygon
}

func compareLand(land, band orb.MultiPolygon) (c landVsBand) {
	b := land.Bound().Union(band.Bound())
	for i := int(math.Floor(b.Min[0] / landCell)); i <= int(math.Floor(b.Max[0]/landCell)); i++ {
		for j := int(math.Floor(b.Min[1] / landCell)); j <= int(math.Floor(b.Max[1]/landCell)); j++ {
			cb := orb.Bound{Min: orb.Point{float64(i) * landCell, float64(j) * landCell}, Max: orb.Point{float64(i+1) * landCell, float64(j+1) * landCell}}
			l, r := cutTo(land, cb), cutTo(band, cb)
			la, ra := acres(l), acres(r)
			c.land += la
			c.band += ra
			switch {
			case len(l) == 0 && len(r) == 0:
				continue
			case len(l) == 0:
				c.bandBeyond += ra
				c.union = append(c.union, r...)
				continue
			case len(r) == 0:
				c.landBeyond += la
				c.union = append(c.union, l...)
				continue
			}
			c.landBeyond += acres(clipOp(polygol.Difference, l, r, l))
			c.bandBeyond += acres(clipOp(polygol.Difference, r, l, r))
			c.union = append(c.union, clipOp(polygol.Union, l, r, append(append(orb.MultiPolygon{}, l...), r...))...)
		}
	}
	return c
}

func cutTo(mp orb.MultiPolygon, b orb.Bound) orb.MultiPolygon {
	var parts orb.MultiPolygon
	for _, p := range mp {
		if len(p) > 0 && p[0].Bound().Intersects(b) {
			parts = append(parts, p)
		}
	}
	if len(parts) == 0 {
		return nil
	}
	var out orb.MultiPolygon
	for _, p := range clip.MultiPolygon(b, orb.Clone(parts).(orb.MultiPolygon)) {
		if len(p) > 0 && len(p[0]) >= 4 {
			out = append(out, p)
		}
	}
	return out
}

// clipOp runs a clipping operation, falling back to ifOdd when the clipping
// library gives up on odd geometry.
func clipOp(op func(polygol.Geom, ...polygol.Geom) (polygol.Geom, error), a, b, ifOdd orb.MultiPolygon) (out orb.MultiPolygon) {
	defer func() {
		if r := recover(); r != nil {
			out = ifOdd
		}
	}()
	g, err := op(geomOf(a), geomOf(b))
	if err != nil {
		return ifOdd
	}
	for _, poly := range g {
		p := make(orb.Polygon, len(poly))
		for i, ring := range poly {
			p[i] = make(orb.Ring, len(ring))
			for k, pt := range ring {
				p[i][k] = orb.Point{pt[0], pt[1]}
			}
		}
		out = append(out, p)
	}
	return out
}

func geomOf(mp orb.MultiPolygon) polygol.Geom {
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

func acres(mp orb.MultiPolygon) float64 {
	a := 0.0
	for _, p := range mp {
		lat := p.Bound().Center()[1] * math.Pi / 180
		a += planar.Area(p) * 111320 * math.Cos(lat) * 110574
	}
	return a * acresPerSqM
}

func printLand(cs []landVsBand) {
	sort.Slice(cs, func(i, j int) bool { return cs[i].landBeyond > cs[j].landBeyond })
	fmt.Println("\n== Trail parks with PAD-US land: land vs the 100 ft band (acres)")
	fmt.Println("ref | park | PAD-US unit [manager designation layer] | land | band | land outside band | band outside land")
	for _, c := range cs {
		fmt.Printf("%s | %s | %s [%s %s %s] | %.0f | %.0f | %.0f | %.0f\n",
			c.p.Ref, c.p.Name, c.unit, c.manager, c.desig, c.layer, c.land, c.band, c.landBeyond, c.bandBeyond)
	}
}
