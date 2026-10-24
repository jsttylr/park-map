package main

import (
	"log"

	"github.com/paulmach/orb"
	"github.com/paulmach/orb/geojson"
)

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
		// A confident official outline, or a trail's route, wins. A weak
		// official match gives way only to a better-named OpenStreetMap one.
		switch c := p.best; {
		case c == nil, c.confidence() == "none":
		case c.confidence() == "medium" && score > c.Score:
		default:
			return
		}
		p.geom = mp
		p.best = &candidate{
			Key: "osm:" + prop(f, "osm"), Unit: prop(f, "osm_name"), Manager: "OSM",
			Designation: "PARK", Layer: "OSM", Score: score, Contains: contains, Extra: true,
		}
		n++
	})
	if err != nil {
		return n, err
	}
	log.Printf("OpenStreetMap: %d parks drawn with an OSM outline", n)
	return n, nil
}
