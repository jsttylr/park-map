package places

import (
	"sync"

	"github.com/paulmach/orb"
	"github.com/paulmach/orb/encoding/mvt"
	"github.com/paulmach/orb/geojson"
	"github.com/paulmach/orb/maptile"
	"github.com/paulmach/orb/simplify"
)

// Zoom levels at which each tile layer starts.
const (
	OutlineZoom = 6 // park outlines
	ZoneZoom    = 9 // summit activation zones
	SummitZoom  = 5 // summit points
	RegionZoom  = 5 // n-fer overlap patches
)

// lodTolerance is the simplification, in degrees, for each detail level.
var lodTolerance = []float64{0.002, 0.0004}

func lodFor(z maptile.Zoom) int {
	switch {
	case z < 8:
		return 0
	case z < 11:
		return 1
	}
	return -1 // full detail
}

// PrepareTiles makes the simplified outlines used for zoomed-out tiles.
func (s *Set) PrepareTiles() {
	if s.Regions != nil {
		s.Regions.PrepareTiles()
	}
	parallel(len(s.Places), func(i int) {
		p := s.Places[i]
		if p.Shape == nil {
			return
		}
		p.lod = make([]orb.MultiPolygon, len(lodTolerance))
		for l, tol := range lodTolerance {
			mp := simplify.DouglasPeucker(tol).Simplify(orb.Clone(p.Shape.MP))
			if mp, ok := mp.(orb.MultiPolygon); ok {
				p.lod[l] = mp
			}
		}
	})
}

// Tile renders one gzipped Mapbox vector tile with layers "parks" (outlines),
// "zones" (activation zones), "nfer" (overlap patches), "park_points" and
// "summits".
func (s *Set) Tile(t maptile.Tile) ([]byte, error) {
	bound := t.Bound(0.05)
	lod := lodFor(t.Z)
	fc := map[string]*geojson.FeatureCollection{
		"parks": geojson.NewFeatureCollection(), "zones": geojson.NewFeatureCollection(),
		"park_points": geojson.NewFeatureCollection(), "summits": geojson.NewFeatureCollection(),
		"nfer": geojson.NewFeatureCollection(),
	}
	if s.Regions != nil && t.Z >= RegionZoom {
		for _, r := range s.Regions.Within(bound) {
			if mp := clipParts(r, lod, bound); len(mp) > 0 {
				f := geojson.NewFeature(mp)
				f.Properties = geojson.Properties{"nfer": r.NFer, "nfer_all": r.NFerAll, "refs": r.Ref}
				fc["nfer"].Append(f)
			}
		}
	}
	for _, p := range s.Within(bound) {
		props := geojson.Properties{
			"id": p.ID, "ref": p.Ref, "name": p.Name, "nfer": p.NFer, "nfer_all": p.NFerAll,
			"outline": p.Shape != nil,
		}
		outlineLayer, pointLayer, minOutline, minPoint := "parks", "park_points", maptile.Zoom(OutlineZoom), maptile.Zoom(0)
		if p.Kind == Summit {
			outlineLayer, pointLayer, minOutline, minPoint = "zones", "summits", ZoneZoom, SummitZoom
			props["points"] = p.Props["points"]
			props["alt_ft"] = p.Props["alt_ft"]
		}
		if p.Shape != nil && t.Z >= minOutline {
			if mp := clipParts(p, lod, bound); len(mp) > 0 {
				f := geojson.NewFeature(mp)
				f.Properties = props
				fc[outlineLayer].Append(f)
			}
		}
		if t.Z >= minPoint && bound.Contains(p.Pt) {
			f := geojson.NewFeature(p.Pt)
			f.Properties = props
			fc[pointLayer].Append(f)
		}
	}
	layers := mvt.NewLayers(fc)
	for _, l := range layers {
		l.Version = 2
	}
	layers.ProjectToTile(t)
	layers.Clip(mvt.MapboxGLDefaultExtentBound)
	for _, l := range layers {
		// Clipping can leave empty polygons, which the simplifier can't handle.
		kept := l.Features[:0]
		for _, f := range l.Features {
			switch g := f.Geometry.(type) {
			case orb.Polygon:
				f.Geometry = clean(orb.MultiPolygon{g})
			case orb.MultiPolygon:
				f.Geometry = clean(g)
			}
			if mp, ok := f.Geometry.(orb.MultiPolygon); ok && len(mp) == 0 || f.Geometry == nil {
				continue
			}
			kept = append(kept, f)
		}
		l.Features = kept
	}
	layers.Simplify(simplify.DouglasPeucker(1.0))
	layers.RemoveEmpty(1.0, 1.0)
	return mvt.MarshalGzipped(layers)
}

// clipParts copies the polygons of p's outline that reach into b.
func clipParts(p *Place, lod int, b orb.Bound) orb.MultiPolygon {
	src := p.Shape.MP
	if lod >= 0 && p.lod != nil && p.lod[lod] != nil {
		src = p.lod[lod]
	}
	var out orb.MultiPolygon
	for _, poly := range src {
		if len(poly) > 0 && len(poly[0]) > 3 && poly[0].Bound().Intersects(b) {
			out = append(out, orb.Clone(poly).(orb.Polygon))
		}
	}
	return out
}

// TileCache keeps rendered tiles. The data never changes while running, so
// entries never go stale; the cache just empties itself when it gets big.
type TileCache struct {
	mu    sync.Mutex
	tiles map[maptile.Tile][]byte
	Max   int
}

func (c *TileCache) Get(s *Set, t maptile.Tile) ([]byte, error) {
	c.mu.Lock()
	b, ok := c.tiles[t]
	c.mu.Unlock()
	if ok {
		return b, nil
	}
	b, err := s.Tile(t)
	if err != nil {
		return nil, err
	}
	c.mu.Lock()
	if c.tiles == nil || len(c.tiles) >= max(c.Max, 1000) {
		c.tiles = map[maptile.Tile][]byte{}
	}
	c.tiles[t] = b
	c.mu.Unlock()
	return b, nil
}
