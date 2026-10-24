// Command fetchlayers downloads the national agency maps that fill in parks
// PAD-US doesn't draw on their own: BLM recreation and herd management areas,
// FWS refuges and hatcheries, Army Corps and Reclamation recreation areas,
// and the official routes of the national scenic and historic trails.
//
// Areas are written with the same properties as PAD-US (Unit_Nm, Loc_Nm,
// Mang_Name, Des_Tp, FeatClass, State_Nm), so builddata reads them as more
// PAD-US layers. Trail routes are written as named lines for fetchtrails.
// Every source is a public ArcGIS map service; see sources.go.
//
//	fetchlayers -areas agency-areas.geojsonl.gz -lines agency-lines.geojsonl.gz
//	fetchlayers -state -areas state-areas.geojsonl.gz -lines /dev/null   # the states' own maps (needs GDAL)
package main

import (
	"bufio"
	"bytes"
	"compress/gzip"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"
	"unicode"
)

// source is one layer of one map service.
type source struct {
	Label string // printed in the log
	URL   string // the layer, ending in /FeatureServer/N or /MapServer/N
	Zip   bool   // URL is instead a zipped shapefile, read with GDAL's ogr2ogr
	Where string // which features to take (SQL), default all

	// Name builds the unit name from a feature's attributes. Loc, if set,
	// names the local piece (PAD-US's Loc_Nm).
	Name, Loc func(a attrs) string
	State     string // the two-letter state of a state's own map, or the attribute holding it

	Manager, Designation, Class string // written as Mang_Name, Des_Tp and FeatClass
	Lines                       bool   // a trail route rather than an area
}

type attrs map[string]any

// s returns an attribute as trimmed text.
func (a attrs) s(k string) string {
	switch v := a[k].(type) {
	case string:
		return strings.TrimSpace(v)
	case float64:
		return strconv.FormatFloat(v, 'f', -1, 64)
	}
	return ""
}

// field returns a Name function that reads one attribute, adding suffix
// unless the name already ends with it.
func field(k, suffix string) func(attrs) string {
	return func(a attrs) string {
		n := a.s(k)
		if n == "" || suffix == "" || strings.HasSuffix(strings.ToLower(n), strings.ToLower(suffix)) {
			return n
		}
		return n + " " + suffix
	}
}

// titled turns an all-capitals name ("CHINCOTEAGUE NATIONAL WILDLIFE
// REFUGE") into ordinary capitals for the map.
func titled(s string) string {
	if s != strings.ToUpper(s) {
		return s
	}
	small := map[string]bool{"of": true, "and": true, "the": true, "at": true, "on": true, "de": true}
	ws := strings.Fields(strings.ToLower(s))
	for i, w := range ws {
		if i > 0 && small[w] {
			continue
		}
		r := []rune(w)
		r[0] = unicode.ToUpper(r[0])
		ws[i] = string(r)
	}
	return strings.Join(ws, " ")
}

var client = &http.Client{Timeout: 5 * time.Minute}

// userAgent is a browser's, since some services (Tennessee's) refuse Go's.
const userAgent = "Mozilla/5.0"

// get runs one request, waiting and retrying while the service is busy.
func get(u string, v url.Values) ([]byte, error) {
	var last error
	for attempt := 0; attempt < 6; attempt++ {
		if attempt > 0 {
			wait := time.Duration(min(5*(1<<attempt), 120)) * time.Second
			log.Printf("retry in %v: %v", wait, last)
			time.Sleep(wait)
		}
		// A GET where the query fits in the URL: some services (Tennessee's)
		// refuse a POST from a data center.
		q := v.Encode()
		req, err := http.NewRequest("GET", u+"?"+q, nil)
		if len(q) > 4000 {
			req, err = http.NewRequest("POST", u, strings.NewReader(q))
		}
		if err != nil {
			return nil, err
		}
		if req.Method == "POST" {
			req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		}
		req.Header.Set("User-Agent", userAgent)
		resp, err := client.Do(req)
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
		if bytes.HasPrefix(bytes.TrimSpace(body), []byte(`{"error"`)) {
			last = fmt.Errorf("%.300s", body)
			continue
		}
		return body, nil
	}
	return nil, last
}

// ids lists the object IDs of the features a source takes.
func ids(s source) ([]int, error) {
	v := url.Values{"where": {s.Where}, "returnIdsOnly": {"true"}, "f": {"json"}}
	body, err := get(s.URL+"/query", v)
	if err != nil {
		return nil, err
	}
	var r struct {
		ObjectIDs []int `json:"objectIds"`
	}
	if err := json.Unmarshal(body, &r); err != nil {
		return nil, err
	}
	return r.ObjectIDs, nil
}

type feature struct {
	Type       string          `json:"type"`
	Geometry   json.RawMessage `json:"geometry"`
	Properties attrs           `json:"properties"`
}

// fetch writes every feature of a source, in batches of object IDs.
func fetch(s source, w io.Writer, batch int, simplify float64) (int, error) {
	if s.Where == "" {
		s.Where = "1=1"
	}
	all, err := ids(s)
	if err != nil {
		return 0, err
	}
	n := 0
	for i := 0; i < len(all); i += batch {
		part := all[i:min(i+batch, len(all))]
		strs := make([]string, len(part))
		for j, id := range part {
			strs[j] = strconv.Itoa(id)
		}
		v := url.Values{
			"objectIds": {strings.Join(strs, ",")}, "outFields": {"*"}, "outSR": {"4326"},
			"maxAllowableOffset": {strconv.FormatFloat(simplify, 'f', -1, 64)},
			"geometryPrecision":  {"5"}, "f": {"geojson"},
		}
		body, err := get(s.URL+"/query", v)
		if err != nil {
			return n, err
		}
		var fc struct {
			Features []feature `json:"features"`
		}
		if err := json.Unmarshal(body, &fc); err != nil {
			return n, fmt.Errorf("%v: %.200s", err, body)
		}
		for _, f := range fc.Features {
			ok, err := emit(s, f, w)
			if err != nil {
				return n, err
			}
			if ok {
				n++
			}
		}
	}
	return n, nil
}

// emit writes one feature with PAD-US style properties, reporting whether it
// had a geometry and a name to write.
func emit(s source, f feature, w io.Writer) (bool, error) {
	if len(f.Geometry) == 0 || string(f.Geometry) == "null" {
		return false, nil
	}
	name := titled(s.Name(f.Properties))
	if name == "" {
		return false, nil
	}
	p := attrs{"Unit_Nm": name, "Mang_Name": s.Manager, "Des_Tp": s.Designation, "FeatClass": s.Class, "Source": s.Label}
	if s.Loc != nil {
		p["Loc_Nm"] = titled(s.Loc(f.Properties))
	}
	switch {
	case len(s.State) == 2: // a state's own map
		p["State_Nm"] = s.State
	case s.State != "":
		p["State_Nm"] = f.Properties.s(s.State)
	}
	b, err := json.Marshal(feature{"Feature", f.Geometry, p})
	if err != nil {
		return false, err
	}
	w.Write(b)
	w.Write([]byte{'\n'})
	return true, nil
}

// fetchZip downloads a zipped shapefile and writes its features, read with
// GDAL's ogr2ogr, which also moves them to longitude and latitude.
func fetchZip(s source, w io.Writer) (int, error) {
	tmp, err := os.CreateTemp("", "layer-*.zip")
	if err != nil {
		return 0, err
	}
	defer os.Remove(tmp.Name())
	req, err := http.NewRequest("GET", s.URL, nil)
	if err != nil {
		return 0, err
	}
	req.Header.Set("User-Agent", userAgent)
	resp, err := client.Do(req)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return 0, fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	if _, err := io.Copy(tmp, resp.Body); err != nil {
		return 0, err
	}
	tmp.Close()
	args := []string{"-f", "GeoJSONSeq", "/vsistdout/", "/vsizip/" + tmp.Name(),
		"-t_srs", "EPSG:4326", "-lco", "COORDINATE_PRECISION=5", "-lco", "RS=NO"}
	if s.Where != "" {
		args = append(args, "-where", s.Where)
	}
	cmd := exec.Command("ogr2ogr", args...)
	cmd.Stderr = os.Stderr
	out, err := cmd.StdoutPipe()
	if err != nil {
		return 0, err
	}
	if err := cmd.Start(); err != nil {
		return 0, err
	}
	n := 0
	sc := bufio.NewScanner(out)
	sc.Buffer(make([]byte, 1<<20), 1<<28)
	for sc.Scan() {
		var f feature
		if json.Unmarshal(sc.Bytes(), &f) != nil {
			continue
		}
		ok, err := emit(s, f, w)
		if err != nil {
			cmd.Process.Kill()
			return n, err
		}
		if ok {
			n++
		}
	}
	if err := sc.Err(); err != nil {
		cmd.Process.Kill()
		return n, err
	}
	return n, cmd.Wait()
}

func create(path string) (*os.File, *gzip.Writer, *bufio.Writer, error) {
	f, err := os.Create(path)
	if err != nil {
		return nil, nil, nil, err
	}
	gz := gzip.NewWriter(f)
	return f, gz, bufio.NewWriter(gz), nil
}

func main() {
	areas := flag.String("areas", "agency-areas.geojsonl.gz", "output areas, PAD-US style (gzipped GeoJSON lines)")
	lines := flag.String("lines", "agency-lines.geojsonl.gz", "output trail routes (gzipped GeoJSON lines)")
	only := flag.String("only", "", "fetch only sources whose label contains this")
	stateMaps := flag.Bool("state", false, "fetch the states' own maps instead of the national ones")
	flag.Parse()

	af, agz, aw, err := create(*areas)
	if err != nil {
		log.Fatal(err)
	}
	lf, lgz, lw, err := create(*lines)
	if err != nil {
		log.Fatal(err)
	}
	failed := 0
	for _, s := range sources {
		if *only != "" && !strings.Contains(s.Label, *only) || *stateMaps != (len(s.State) == 2) {
			continue
		}
		start := time.Now()
		w, batch, simplify := aw, 100, 0.0001
		if s.Lines {
			w, batch, simplify = lw, 50, 0.00005
		}
		var n int
		if s.Zip {
			n, err = fetchZip(s, w)
		} else {
			n, err = fetch(s, w, batch, simplify)
		}
		if err != nil {
			// One agency's service being down shouldn't stop the build;
			// those parks just stay as they were.
			log.Printf("%s: %v (kept %d features)", s.Label, err, n)
			failed++
		}
		log.Printf("%-45s %6d features in %v", s.Label, n, time.Since(start).Round(time.Second))
	}
	for _, c := range []struct {
		f  *os.File
		gz *gzip.Writer
		w  *bufio.Writer
	}{{af, agz, aw}, {lf, lgz, lw}} {
		if err := c.w.Flush(); err != nil {
			log.Fatal(err)
		}
		if err := c.gz.Close(); err != nil {
			log.Fatal(err)
		}
		c.f.Close()
	}
	if failed > 0 {
		log.Printf("%d sources failed", failed)
	}
}
