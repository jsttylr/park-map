package main

import (
	"log"

	"github.com/paulmach/orb"
	"github.com/paulmach/orb/geojson"
)

// osmSlack is how much worse an OpenStreetMap area's name may match than a
// weak official outline's, when the area holds the pin and the outline doesn't.
const osmSlack = 0.1

// useOSM gives a park the outline cmd/fetchosmparks found in OpenStreetMap,
// where PAD-US and the agency maps have nothing better: pieces of bigger
// units, city and county parks, and places too new for PAD-US.
func useOSM(path string, parks []*park) (int, error) {
	byRef := map[string]*park{}
	for _, p := range parks {
		byRef[p.Ref] = p
	}
	n := 0
	err := eachFeature(path, func(f *geojson.Feature, mp orb.MultiPolygon) {
		p := byRef[prop(f, "ref")]
		if p == nil {
			return
		}
		score, _ := f.Properties["score"].(float64)
		contains, _ := f.Properties["contains"].(bool)
		km, _ := f.Properties["km"].(float64)
		o := &candidate{
			Key: "osm:" + prop(f, "osm"), Unit: prop(f, "osm_name"), Manager: "OSM",
			Designation: "PARK", Layer: "OSM", Score: score, Contains: contains, DistKm: km, Extra: true,
		}
		// A confident official outline, or a trail's route, wins. A weak
		// official match gives way to a better-named OpenStreetMap area, or
		// to a nearly as well named one that confidently holds the pin
		// ("Savage Gulf State Park" for Savage Gulf State Natural Area).
		// Anything less and it's often the unit next door (Torrey Pines State
		// Natural Reserve for Torrey Pines State Beach).
		switch c := p.best; {
		case c == nil, c.confidence() == "none":
		case c.confidence() == "medium" && (score > c.Score || o.confidence() == "high" && score >= c.Score-osmSlack):
		default:
			return
		}
		p.geom = mp
		p.best = o
		n++
	})
	if err != nil {
		return n, err
	}
	log.Printf("OpenStreetMap: %d parks drawn with an OSM outline", n)
	return n, nil
}
