package main

import (
	"math"
	"testing"

	"github.com/paulmach/orb"
)

func square(x0, y0, x1, y1 float64) orb.Polygon {
	return orb.Polygon{{{x0, y0}, {x1, y0}, {x1, y1}, {x0, y1}, {x0, y0}}}
}

func TestCompareLand(t *testing.T) {
	// Land 0.02 deg wide, band 0.01 deg wide sticking half out of it, both
	// crossing a cell edge at 0.1.
	land := orb.MultiPolygon{square(0.05, 0.05, 0.07, 0.15)}
	band := orb.MultiPolygon{square(0.065, 0.05, 0.075, 0.15)}
	c := compareLand(land, band)
	ratio := func(a, b float64) float64 { return a / b }
	if r := ratio(c.landBeyond, c.land); math.Abs(r-0.75) > 0.01 {
		t.Errorf("land outside band = %.3f of land, want 0.75", r)
	}
	if r := ratio(c.bandBeyond, c.band); math.Abs(r-0.5) > 0.01 {
		t.Errorf("band outside land = %.3f of band, want 0.5", r)
	}
	if r := ratio(acres(c.union), c.land); math.Abs(r-1.25) > 0.01 {
		t.Errorf("union = %.3f of land, want 1.25", r)
	}
}
