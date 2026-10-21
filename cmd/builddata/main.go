// Command builddata matches US POTA parks to PAD-US boundary polygons.
//
// It reads the POTA park list (all_parks_ext.csv), asks the USGS PAD-US 4.1
// web service which protected areas sit at or near each park's point, picks
// the one whose name best matches, then downloads every parcel of that unit.
// Output is a gzipped GeoJSON file with one feature per park (its boundary,
// or just its point when no boundary matched) and a CSV report for checking
// match quality.
package main

import (
	"bufio"
	"compress/gzip"
	"encoding/csv"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log"
	"math"
	"net/http"
	"net/url"
	"os"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/jsttylr/park-map/internal/match"
	"github.com/paulmach/orb"
	"github.com/paulmach/orb/geojson"
)

// The PAD-US 4.1 combined inventory (fee, designation, proclamation, easement, marine).
const padusURL = "https://services.arcgis.com/v01gqwM5QqNysAAi/arcgis/rest/services/Protection_Mechanism_Category_PADUS/FeatureServer/0/query"

const (
	nearMeters = 2000 // how far from a park point a polygon may sit and still be a candidate
	gatherDeg  = 3.0  // search box half-width when collecting parcels of the chosen unit
	simplify   = 0.00005
	minHigh    = 0.5
	minMedium  = 0.3
	minNearMed = 0.75
	classes    = "FeatClass IN ('Fee','Designation','Proclamation')"
)

type park struct {
	Ref, Name, Loc string
	Pt             orb.Point
	best           *candidate
	geom           orb.MultiPolygon
}

type candidate struct {
	Unit, Manager, Designation, Class string
	Score                             float64
	Contains                          bool
}

func (c *candidate) rank() float64 {
	if c.Contains {
		return c.Score + 0.15
	}
	return c.Score
}

func (c *candidate) confidence() string {
	switch {
	case c == nil:
		return "none"
	case c.Contains && c.Score >= minHigh:
		return "high"
	case c.Contains && c.Score >= minMedium, !c.Contains && c.Score >= minNearMed:
		return "medium"
	}
	return "none"
}

var client = &http.Client{Timeout: 120 * time.Second}

// query calls the PAD-US service, retrying on errors and throttling.
func query(params url.Values, out any) error {
	params.Set("f", firstNonEmpty(params.Get("f"), "json"))
	var last error
	for attempt := 0; attempt < 6; attempt++ {
		if attempt > 0 {
			time.Sleep(time.Duration(1<<attempt) * time.Second)
		}
		resp, err := client.PostForm(padusURL, params)
		if err != nil {
			last = err
			continue
		}
		body, err := io.ReadAll(resp.Body)
		resp.Body.Close()
		if err != nil {
			last = err
			continue
		}
		if resp.StatusCode != 200 {
			last = fmt.Errorf("HTTP %d", resp.StatusCode)
			continue
		}
		var e struct {
			Error *struct {
				Code    int
				Message string
			}
		}
		if json.Unmarshal(body, &e) == nil && e.Error != nil {
			last = fmt.Errorf("service error %d: %s", e.Error.Code, e.Error.Message)
			continue
		}
		return json.Unmarshal(body, out)
	}
	return last
}

func firstNonEmpty(a, b string) string {
	if a != "" {
		return a
	}
	return b
}

func pointParams(p orb.Point, meters int) url.Values {
	v := url.Values{}
	v.Set("where", classes)
	v.Set("geometry", fmt.Sprintf("%f,%f", p[0], p[1]))
	v.Set("geometryType", "esriGeometryPoint")
	v.Set("inSR", "4326")
	v.Set("spatialRel", "esriSpatialRelIntersects")
	if meters > 0 {
		v.Set("distance", strconv.Itoa(meters))
		v.Set("units", "esriSRUnit_Meter")
	}
	return v
}

type attrs struct {
	OBJECTID                                      int64
	Unit_Nm, Loc_Nm, Mang_Name, Des_Tp, FeatClass string
}

// findCandidate picks the best-named protected area at or near a park's point.
func findCandidate(p *park) error {
	near := pointParams(p.Pt, nearMeters)
	near.Set("outFields", "OBJECTID,Unit_Nm,Loc_Nm,Mang_Name,Des_Tp,FeatClass")
	near.Set("returnGeometry", "false")
	var nr struct{ Features []struct{ Attributes attrs } }
	if err := query(near, &nr); err != nil {
		return err
	}
	if len(nr.Features) == 0 {
		return nil
	}
	in := pointParams(p.Pt, 0)
	in.Set("returnIdsOnly", "true")
	var ir struct{ ObjectIds []int64 }
	if err := query(in, &ir); err != nil {
		return err
	}
	inside := map[int64]bool{}
	for _, id := range ir.ObjectIds {
		inside[id] = true
	}
	for _, f := range nr.Features {
		a := f.Attributes
		if a.Unit_Nm == "" {
			continue
		}
		s := match.Similarity(p.Name, a.Unit_Nm)
		if a.Loc_Nm != "" {
			s = math.Max(s, match.Similarity(p.Name, a.Loc_Nm))
		}
		c := &candidate{Unit: a.Unit_Nm, Manager: a.Mang_Name, Designation: a.Des_Tp, Class: a.FeatClass, Score: s, Contains: inside[a.OBJECTID]}
		if p.best == nil || c.rank() > p.best.rank() {
			p.best = c
		}
	}
	return nil
}

func sqlQuote(s string) string { return "'" + strings.ReplaceAll(s, "'", "''") + "'" }

// fetchUnit downloads every parcel of the chosen unit within a few degrees of the park.
func fetchUnit(p *park) error {
	c := p.best
	where := fmt.Sprintf("Unit_Nm = %s AND Mang_Name = %s AND FeatClass = %s", sqlQuote(c.Unit), sqlQuote(c.Manager), sqlQuote(c.Class))
	for offset := 0; ; {
		v := url.Values{}
		v.Set("where", where)
		v.Set("geometry", fmt.Sprintf("%f,%f,%f,%f", p.Pt[0]-gatherDeg, p.Pt[1]-gatherDeg, p.Pt[0]+gatherDeg, p.Pt[1]+gatherDeg))
		v.Set("geometryType", "esriGeometryEnvelope")
		v.Set("inSR", "4326")
		v.Set("spatialRel", "esriSpatialRelIntersects")
		v.Set("outSR", "4326")
		v.Set("outFields", "OBJECTID")
		v.Set("maxAllowableOffset", strconv.FormatFloat(simplify, 'f', -1, 64))
		v.Set("geometryPrecision", "5")
		v.Set("resultOffset", strconv.Itoa(offset))
		v.Set("resultRecordCount", "500")
		v.Set("orderByFields", "OBJECTID")
		v.Set("f", "geojson")
		var fc struct {
			Features         []json.RawMessage                    `json:"features"`
			ExceededTransfer bool                                 `json:"exceededTransferLimit"`
			Properties       struct{ ExceededTransferLimit bool } `json:"properties"`
		}
		if err := query(v, &fc); err != nil {
			return err
		}
		for _, raw := range fc.Features {
			f, err := geojson.UnmarshalFeature(raw)
			if err != nil || f.Geometry == nil {
				continue
			}
			switch g := f.Geometry.(type) {
			case orb.Polygon:
				p.geom = append(p.geom, g)
			case orb.MultiPolygon:
				p.geom = append(p.geom, g...)
			}
		}
		if len(fc.Features) == 0 || !(fc.ExceededTransfer || fc.Properties.ExceededTransferLimit) {
			return nil
		}
		offset += len(fc.Features)
	}
}

func main() {
	potaPath := flag.String("pota", "all_parks_ext.csv", "POTA park list CSV")
	prefix := flag.String("prefix", "US-", "park reference prefix to include")
	only := flag.String("only", "", "only parks in this location, e.g. US-ME (for testing)")
	limit := flag.Int("limit", 0, "only the first N parks (for testing)")
	workers := flag.Int("workers", 8, "parallel requests")
	out := flag.String("out", "us-parks.geojson.gz", "output GeoJSON (gzipped)")
	report := flag.String("report", "report.csv", "output match report CSV")
	flag.Parse()

	parks, err := readPOTA(*potaPath, *prefix, *only)
	if err != nil {
		log.Fatal(err)
	}
	if *limit > 0 && len(parks) > *limit {
		parks = parks[:*limit]
	}
	log.Printf("%d active parks", len(parks))

	run(parks, *workers, "matching", findCandidate)
	var matched []*park
	for _, p := range parks {
		if p.best.confidence() != "none" {
			matched = append(matched, p)
		}
	}
	run(matched, *workers, "boundaries", fetchUnit)

	if err := writeGeoJSON(*out, parks); err != nil {
		log.Fatal(err)
	}
	if err := writeReport(*report, parks); err != nil {
		log.Fatal(err)
	}
	summarize(parks)
}

// run applies fn to every park with a pool of workers, logging progress.
func run(parks []*park, workers int, label string, fn func(*park) error) {
	jobs := make(chan *park)
	var wg sync.WaitGroup
	var mu sync.Mutex
	done, failed := 0, 0
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for p := range jobs {
				err := fn(p)
				mu.Lock()
				done++
				if err != nil {
					failed++
					log.Printf("%s %s: %v", label, p.Ref, err)
				}
				if done%1000 == 0 {
					log.Printf("%s: %d/%d", label, done, len(parks))
				}
				mu.Unlock()
			}
		}()
	}
	for _, p := range parks {
		jobs <- p
	}
	close(jobs)
	wg.Wait()
	log.Printf("%s: done, %d failed", label, failed)
}

func readPOTA(path, prefix, only string) ([]*park, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	r := csv.NewReader(f)
	head, err := r.Read()
	if err != nil {
		return nil, err
	}
	col := map[string]int{}
	for i, h := range head {
		col[h] = i
	}
	for _, need := range []string{"reference", "name", "active", "locationDesc", "latitude", "longitude"} {
		if _, ok := col[need]; !ok {
			return nil, fmt.Errorf("POTA CSV missing column %q", need)
		}
	}
	var parks []*park
	for {
		rec, err := r.Read()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, err
		}
		ref, loc := rec[col["reference"]], rec[col["locationDesc"]]
		if !strings.HasPrefix(ref, prefix) || rec[col["active"]] != "1" || (only != "" && !strings.Contains(loc, only)) {
			continue
		}
		lat, err1 := strconv.ParseFloat(rec[col["latitude"]], 64)
		lon, err2 := strconv.ParseFloat(rec[col["longitude"]], 64)
		if err1 != nil || err2 != nil {
			continue
		}
		parks = append(parks, &park{Ref: ref, Name: rec[col["name"]], Loc: loc, Pt: orb.Point{lon, lat}})
	}
	return parks, nil
}

func writeGeoJSON(path string, parks []*park) error {
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()
	gz := gzip.NewWriter(f)
	w := bufio.NewWriter(gz)
	w.WriteString(`{"type":"FeatureCollection","features":[`)
	for i, p := range parks {
		var g orb.Geometry = p.Pt
		if len(p.geom) > 0 {
			g = p.geom
		}
		ft := geojson.NewFeature(g)
		ft.Properties["ref"] = p.Ref
		ft.Properties["name"] = p.Name
		ft.Properties["loc"] = p.Loc
		ft.Properties["lat"] = p.Pt[1]
		ft.Properties["lon"] = p.Pt[0]
		ft.Properties["match"] = p.best.confidence()
		if len(p.geom) > 0 {
			ft.Properties["boundary_name"] = p.best.Unit
			ft.Properties["manager"] = p.best.Manager
			ft.Properties["designation"] = p.best.Designation
		}
		b, err := json.Marshal(ft)
		if err != nil {
			return err
		}
		if i > 0 {
			w.WriteString(",\n")
		}
		w.Write(b)
	}
	w.WriteString("]}\n")
	if err := w.Flush(); err != nil {
		return err
	}
	return gz.Close()
}

func writeReport(path string, parks []*park) error {
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()
	w := csv.NewWriter(f)
	w.Write([]string{"ref", "name", "loc", "match", "boundary_name", "manager", "designation", "class", "score", "contains", "parts"})
	for _, p := range parks {
		row := []string{p.Ref, p.Name, p.Loc, p.best.confidence(), "", "", "", "", "", "", strconv.Itoa(len(p.geom))}
		if c := p.best; c != nil {
			row[4], row[5], row[6], row[7] = c.Unit, c.Manager, c.Designation, c.Class
			row[8] = strconv.FormatFloat(c.Score, 'f', 2, 64)
			row[9] = strconv.FormatBool(c.Contains)
		}
		w.Write(row)
	}
	w.Flush()
	return w.Error()
}

func summarize(parks []*park) {
	byConf := map[string]int{}
	type st struct{ total, matched int }
	byState := map[string]*st{}
	var none, medium []*park
	noGeom := 0
	for _, p := range parks {
		c := p.best.confidence()
		byConf[c]++
		s := byState[p.Loc]
		if s == nil {
			s = &st{}
			byState[p.Loc] = s
		}
		s.total++
		if c != "none" {
			s.matched++
			if len(p.geom) == 0 {
				noGeom++
			}
		} else {
			none = append(none, p)
		}
		if c == "medium" {
			medium = append(medium, p)
		}
	}
	n := float64(len(parks))
	fmt.Printf("\n== Match summary: %d parks\n", len(parks))
	for _, c := range []string{"high", "medium", "none"} {
		fmt.Printf("%-7s %6d  %5.1f%%\n", c, byConf[c], 100*float64(byConf[c])/n)
	}
	fmt.Printf("matched but no geometry downloaded: %d\n", noGeom)
	var states []string
	for s := range byState {
		states = append(states, s)
	}
	sort.Strings(states)
	fmt.Println("\n== By state (matched/total)")
	for _, s := range states {
		fmt.Printf("%s %d/%d  ", s, byState[s].matched, byState[s].total)
	}
	fmt.Println()
	sample := func(title string, ps []*park) {
		step := max(1, len(ps)/40)
		fmt.Printf("\n== %s (every %dth)\n", title, step)
		for i := 0; i < len(ps); i += step {
			p := ps[i]
			if c := p.best; c != nil {
				fmt.Printf("%s %s | %s [%s] (%.2f, inside=%v)\n", p.Ref, p.Name, c.Unit, c.Class, c.Score, c.Contains)
			} else {
				fmt.Printf("%s %s | -\n", p.Ref, p.Name)
			}
		}
	}
	sample("Medium matches", medium)
	sample("No boundary", none)
}
