package main

import (
	"encoding/csv"
	"fmt"
	"os"
	"strconv"
	"sort"
	"strings"
	"time"

	"github.com/jsttylr/park-map/internal/places"
	"github.com/paulmach/orb"
	"github.com/paulmach/orb/maptile"
)

// runSelftest prints a few lookups and tile timings so a build log shows
// whether the data and overlaps look sane.
func runSelftest(s *places.Set, explain string) {
	for _, ref := range strings.Split(explain, ",") {
		for _, p := range s.Search(strings.TrimSpace(ref), 1) {
			fmt.Printf("%s %q at %.5f,%.5f in %s\n", p.Ref, p.Name, p.Pt[1], p.Pt[0], p.Loc)
			fmt.Printf("  boundary %v, designation %v, manager %v, match %v\n", p.Props["boundary_name"], p.Props["designation"], p.Props["manager"], p.Props["match"])
			if p.Shape != nil {
				b := p.Shape.Bound
				fmt.Printf("  %d parts, bounds %.4f,%.4f to %.4f,%.4f (%.0f x %.0f km)\n", len(p.Shape.MP), b.Min[1], b.Min[0], b.Max[1], b.Max[0],
					places.Km(orb.Point{b.Min[0], b.Min[1]}, orb.Point{b.Max[0], b.Min[1]}), places.Km(orb.Point{b.Min[0], b.Min[1]}, orb.Point{b.Min[0], b.Max[1]}))
			}
		}
	}

	spots := []struct {
		name string
		pt   orb.Point
	}{
		{"Mt. Rainier summit", orb.Point{-121.7603, 46.8529}},
		{"Great Smoky Mountains, Clingmans Dome", orb.Point{-83.4985, 35.5628}},
		{"Appalachian Trail at Springer Mountain", orb.Point{-84.1938, 34.6268}},
		{"Shenandoah, Skyline Drive", orb.Point{-78.4, 38.53}},
		{"Times Square (nothing)", orb.Point{-73.9855, 40.758}},
		{"Shenandoah 3-fer (38.73061,-78.32298)", orb.Point{-78.32298, 38.73061}},
	}
	for _, sp := range spots {
		in, near := s.At(sp.pt, 2)
		fmt.Printf("%-42s %s\n", sp.name, places.Summary(in, near, 160))
		for _, r := range s.Regions.Places {
			if r.Shape.Contains(sp.pt) {
				fmt.Printf("%-42s   patch %s (%d parks, %d with summits)\n", "", r.Ref, r.NFer, r.NFerAll)
			}
		}
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

	byN := map[int]int{}
	for _, r := range s.Regions.Places {
		byN[min(r.NFer, 6)]++
	}
	fmt.Printf("\nOverlap patches by number of parks: %v\n", byN)

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

// writeNFer writes each park's n-fer count, for comparing builds.
func writeNFer(s *places.Set, path string) error {
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()
	w := csv.NewWriter(f)
	w.Write([]string{"ref", "name", "loc", "nfer", "nfer_all"})
	for _, p := range s.Places {
		if p.Kind == places.Park {
			w.Write([]string{p.Ref, p.Name, p.Loc, strconv.Itoa(p.NFer), strconv.Itoa(p.NFerAll)})
		}
	}
	w.Flush()
	return w.Error()
}
