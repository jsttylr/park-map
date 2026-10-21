package main

import (
	"fmt"
	"sort"
	"time"

	"github.com/jsttylr/park-map/internal/places"
	"github.com/paulmach/orb"
	"github.com/paulmach/orb/maptile"
)

// runSelftest prints a few lookups and tile timings so a build log shows
// whether the data and overlaps look sane.
func runSelftest(s *places.Set) {
	spots := []struct {
		name string
		pt   orb.Point
	}{
		{"Mt. Rainier summit", orb.Point{-121.7603, 46.8529}},
		{"Great Smoky Mountains, Clingmans Dome", orb.Point{-83.4985, 35.5628}},
		{"Appalachian Trail at Springer Mountain", orb.Point{-84.1938, 34.6268}},
		{"Shenandoah, Skyline Drive", orb.Point{-78.4, 38.53}},
		{"Times Square (nothing)", orb.Point{-73.9855, 40.758}},
	}
	for _, sp := range spots {
		in, near := s.At(sp.pt, 2)
		fmt.Printf("%-42s %s\n", sp.name, places.Summary(in, near, 160))
	}

	var top []*places.Place
	for _, p := range s.Places {
		if p.Kind == places.Park {
			top = append(top, p)
		}
	}
	sort.Slice(top, func(i, j int) bool { return top[i].NFer > top[j].NFer })
	fmt.Println("\nParks with the biggest n-fer:")
	for _, p := range top[:min(15, len(top))] {
		fmt.Printf("  %d-fer (%d with summits)  %s %s, %s\n", p.NFer, p.NFerAll, p.Ref, p.Name, p.Loc)
	}

	fmt.Println("\nTile timings:")
	for _, tc := range []struct {
		z    maptile.Zoom
		name string
		pt   orb.Point
	}{{3, "US west", orb.Point{-110, 40}}, {6, "Washington", orb.Point{-121, 47}}, {8, "Cascades", orb.Point{-121.7, 46.8}}, {12, "Rainier", orb.Point{-121.76, 46.85}}} {
		t := time.Now()
		b, err := s.Tile(maptile.At(tc.pt, tc.z))
		fmt.Printf("  z%-2d %-12s %7d bytes  %v  %v\n", tc.z, tc.name, len(b), time.Since(t).Round(time.Millisecond), err)
	}
}
