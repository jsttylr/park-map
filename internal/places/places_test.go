package places

import (
	"math"
	"math/rand"
	"testing"

	"github.com/paulmach/orb"
	"github.com/paulmach/orb/encoding/mvt"
	"github.com/paulmach/orb/geojson"
	"github.com/paulmach/orb/maptile"
	"github.com/paulmach/orb/planar"
)

func box(x0, y0, x1, y1 float64) *Shape {
	return newShape(orb.MultiPolygon{{{{x0, y0}, {x1, y0}, {x1, y1}, {x0, y1}, {x0, y0}}}})
}

func testSet() *Set {
	s := &Set{}
	s.Add(&Place{Ref: "US-0001", Name: "Big Forest", Shape: box(0, 0, 1, 1)})
	s.Add(&Place{Ref: "US-0002", Name: "Wilderness", Shape: box(0.2, 0.2, 0.6, 0.6)})
	s.Add(&Place{Ref: "US-0003", Name: "Trail", Shape: box(0.5, 0.5, 1.5, 0.55)})
	s.Add(&Place{Ref: "US-0004", Name: "Next Door", Shape: box(1, 0, 2, 1)})
	s.Add(&Place{Ref: "US-0005", Name: "No Outline", Pt: orb.Point{0.3, 0.3}})
	s.Add(&Place{Kind: Summit, Ref: "W0X/AA-001", Name: "Peak", Shape: box(0.52, 0.51, 0.54, 0.54)})
	return s
}

func TestOverlaps(t *testing.T) {
	s := testSet()
	s.ComputeOverlaps()
	want := []struct{ nfer, all int }{{3, 4}, {3, 4}, {3, 4}, {2, 2}, {1, 1}, {3, 4}}
	for i, w := range want {
		p := s.Places[i]
		if p.NFer != w.nfer || p.NFerAll != w.all {
			t.Errorf("%s: nfer %d/%d, want %d/%d (overlaps %v)", p.Ref, p.NFer, p.NFerAll, w.nfer, w.all, p.Overlaps)
		}
	}
	// Next Door only touches Big Forest along an edge; the trail crosses into it.
	if got := s.Places[3].Overlaps; len(got) != 1 || got[0] != 2 {
		t.Errorf("Next Door overlaps %v, want [2]", got)
	}
}

func TestAt(t *testing.T) {
	s := testSet()
	in, near := s.At(orb.Point{0.53, 0.52}, 2)
	if len(in) != 4 {
		t.Errorf("in = %d places, want 4", len(in))
	}
	if in[len(in)-1].Kind != Summit {
		t.Errorf("summits should sort after parks")
	}
	if len(near) != 0 {
		t.Errorf("near = %v", near)
	}
	_, near = s.At(orb.Point{0.305, 0.3}, 2)
	if len(near) != 1 || near[0].Ref != "US-0005" {
		t.Errorf("near = %v, want US-0005", near)
	}
}

func TestSearch(t *testing.T) {
	s := testSet()
	if got := s.Search("us-0003", 5); len(got) != 1 || got[0].Name != "Trail" {
		t.Errorf("search by ref: %v", got)
	}
	if got := s.Search("forest", 5); len(got) != 1 {
		t.Errorf("search by name: %v", got)
	}
}

func TestTile(t *testing.T) {
	s := testSet()
	s.ComputeOverlaps()
	s.PrepareTiles()
	for _, z := range []maptile.Zoom{3, 7, 10, 14} {
		b, err := s.Tile(maptile.At(orb.Point{0.53, 0.52}, z))
		if err != nil || len(b) < 50 {
			t.Fatalf("zoom %d: %d bytes, %v", z, len(b), err)
		}
		layers, err := mvt.UnmarshalGzipped(b)
		if err != nil {
			t.Fatal(err)
		}
		n := map[string]int{}
		for _, l := range layers {
			n[l.Name] = len(l.Features)
		}
		if z >= ZoneZoom && n["zones"] != 1 || z >= OutlineZoom && n["parks"] < 3 || z < 8 && n["park_points"] < 4 {
			t.Errorf("zoom %d: %v", z, n)
		}
	}
}

func TestSummary(t *testing.T) {
	s := testSet()
	in, near := s.At(orb.Point{0.53, 0.52}, 2)
	got := Summary(in, near, 160)
	want := "3-fer: US-0001 Big Forest, US-0002 Wilderness, US-0003 Trail. Summit W0X/AA-001 Peak."
	if got != want {
		t.Errorf("got  %q\nwant %q", got, want)
	}
	if got := Summary(in, near, 50); len(got) > 50 || got[:6] != "3-fer:" {
		t.Errorf("short: %q", got)
	}
	if got := Summary(nil, nil, 160); got != "No parks here." {
		t.Errorf("empty: %q", got)
	}
}

func TestLongRing(t *testing.T) {
	// A wobbly 5000-point ring, checked against orb's plain point test.
	var r orb.Ring
	for i := 0; i < 5000; i++ {
		a := float64(i) / 5000 * 2 * math.Pi
		rad := 1 + 0.3*math.Sin(a*37)
		r = append(r, orb.Point{rad * math.Cos(a), rad * math.Sin(a)})
	}
	r = append(r, r[0])
	s := newShape(orb.MultiPolygon{{r}})
	rng := rand.New(rand.NewSource(1))
	for i := 0; i < 20000; i++ {
		p := orb.Point{rng.Float64()*3 - 1.5, rng.Float64()*3 - 1.5}
		if got, want := s.Contains(p), planar.RingContains(r, p); got != want {
			t.Fatalf("Contains(%v) = %v, want %v", p, got, want)
		}
	}
}

func TestEmptyParts(t *testing.T) {
	ft := geojson.NewFeature(orb.MultiPolygon{{}, {{{0, 0}, {1, 0}}}, {{{0, 0}, {1, 0}, {1, 1}, {0, 0}}}})
	ft.Properties = geojson.Properties{"ref": "US-9", "lat": 0.5, "lon": 0.6}
	p := fromFeature(ft, Park)
	if p.Shape == nil || len(p.Shape.MP) != 1 {
		t.Fatalf("shape = %+v", p.Shape)
	}
	s := &Set{}
	s.Add(p)
	s.PrepareTiles()
	if _, err := s.Tile(maptile.At(orb.Point{0.5, 0.5}, 8)); err != nil {
		t.Fatal(err)
	}
	empty := geojson.NewFeature(orb.MultiPolygon{{}})
	if fromFeature(empty, Park).Shape != nil {
		t.Error("empty outline should become a point")
	}
}

func TestRegions(t *testing.T) {
	s := testSet()
	s.ComputeOverlaps()
	s.ComputeRegions()
	type want struct{ parks, all int }
	got := map[string]want{}
	for _, r := range s.Regions.Places {
		got[r.Ref] = want{r.NFer, r.NFerAll}
	}
	for ref, w := range map[string]want{
		"US-0001 US-0002":                    {2, 2},
		"US-0001 US-0003":                    {2, 2},
		"US-0003 US-0004":                    {2, 2},
		"US-0001 US-0002 US-0003":            {3, 3},
		"US-0001 US-0002 W0X/AA-001":         {2, 3},
		"US-0001 US-0002 US-0003 W0X/AA-001": {3, 4},
	} {
		if got[ref] != w {
			t.Errorf("%s: got %v, want %v", ref, got[ref], w)
		}
	}
	// Next Door only shares an edge with Big Forest, so no patch for them.
	if _, ok := got["US-0001 US-0004"]; ok {
		t.Error("edge-only neighbours got an overlap patch")
	}
	// The 2-fer patch of the forest and the wilderness is the wilderness box.
	for _, r := range s.Regions.Places {
		if r.Ref == "US-0001 US-0002" {
			if b := r.Shape.Bound; math.Abs(b.Min[0]-0.2) > 1e-9 || math.Abs(b.Max[1]-0.6) > 1e-9 {
				t.Errorf("patch bound %v", b)
			}
		}
	}
	path := t.TempDir() + "/nfer.geojson.gz"
	if err := s.WriteRegions(path); err != nil {
		t.Fatal(err)
	}
	n := len(s.Regions.Places)
	if err := s.LoadRegions(path); err != nil || len(s.Regions.Places) != n {
		t.Fatalf("reload: %d patches, %v", len(s.Regions.Places), err)
	}
}
