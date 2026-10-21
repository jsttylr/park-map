// Command buildsota joins the SOTA summits list with the sotl.as activation
// zones (published monthly by github.com/OffARO/sota-az) into one gzipped
// GeoJSON file: each summit's activation zone, or just its point when there
// is no zone for it.
//
// Activation zones © sotl.as contributors, for free non-commercial use.
package main

import (
	"bufio"
	"compress/gzip"
	"encoding/csv"
	"encoding/json"
	"flag"
	"io"
	"log"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/paulmach/orb"
	"github.com/paulmach/orb/geojson"
)

type summit struct {
	Code, Name, Assoc, Region string
	Pt                        orb.Point
	AltFt, Points, Bonus      int
	Activations               int
	zone                      orb.MultiPolygon
}

func main() {
	list := flag.String("summits", "summitslist.csv", "SOTA summits list CSV")
	azDir := flag.String("az", "az", "folder of <association>.geojson.gz activation zone files")
	out := flag.String("out", "sota.geojson.gz", "output GeoJSON (gzipped)")
	prefixes := flag.String("prefix", "W,K", "association prefixes to keep, comma separated")
	flag.Parse()

	summits, err := readSummits(*list, strings.Split(*prefixes, ","))
	if err != nil {
		log.Fatal(err)
	}
	byCode := map[string]*summit{}
	assocs := map[string]bool{}
	for _, s := range summits {
		byCode[s.Code] = s
		assocs[s.Assoc] = true
	}
	log.Printf("%d valid summits in %d associations", len(summits), len(assocs))

	zones := 0
	for a := range assocs {
		path := filepath.Join(*azDir, strings.ToLower(a)+".geojson.gz")
		n, err := readZones(path, byCode)
		if err != nil {
			log.Printf("%s: %v", a, err)
			continue
		}
		zones += n
	}
	log.Printf("%d summits have an activation zone", zones)

	if err := write(*out, summits); err != nil {
		log.Fatal(err)
	}
}

func readSummits(path string, prefixes []string) ([]*summit, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	br := bufio.NewReader(f)
	// The first line is a title ("SOTA Summits List (Date=...)"), not the header.
	if first, err := br.Peek(4); err == nil && string(first) == "SOTA" {
		br.ReadString('\n')
	}
	r := csv.NewReader(br)
	r.FieldsPerRecord = -1
	head, err := r.Read()
	if err != nil {
		return nil, err
	}
	col := map[string]int{}
	for i, h := range head {
		col[strings.TrimSpace(h)] = i
	}
	get := func(rec []string, name string) string {
		if i, ok := col[name]; ok && i < len(rec) {
			return strings.TrimSpace(rec[i])
		}
		return ""
	}
	num := func(rec []string, name string) int {
		v, _ := strconv.Atoi(get(rec, name))
		return v
	}
	now := time.Now()
	var out []*summit
	for {
		rec, err := r.Read()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, err
		}
		code := get(rec, "SummitCode")
		assoc, _, ok := strings.Cut(code, "/")
		if !ok || !hasPrefix(assoc, prefixes) {
			continue
		}
		if to, err := time.Parse("02/01/2006", get(rec, "ValidTo")); err == nil && to.Before(now) {
			continue // retired summit
		}
		lon, err1 := strconv.ParseFloat(get(rec, "Longitude"), 64)
		lat, err2 := strconv.ParseFloat(get(rec, "Latitude"), 64)
		if err1 != nil || err2 != nil {
			continue
		}
		out = append(out, &summit{
			Code: code, Name: get(rec, "SummitName"), Assoc: assoc, Region: get(rec, "RegionName"),
			Pt: orb.Point{lon, lat}, AltFt: num(rec, "AltFt"), Points: num(rec, "Points"),
			Bonus: num(rec, "BonusPoints"), Activations: num(rec, "ActivationCount"),
		})
	}
	return out, nil
}

func hasPrefix(s string, prefixes []string) bool {
	for _, p := range prefixes {
		if p = strings.TrimSpace(p); p != "" && strings.HasPrefix(s, p) {
			return true
		}
	}
	return false
}

// readZones attaches the activation zones in one association file to their summits.
func readZones(path string, byCode map[string]*summit) (int, error) {
	f, err := os.Open(path)
	if err != nil {
		return 0, err
	}
	defer f.Close()
	gz, err := gzip.NewReader(f)
	if err != nil {
		return 0, err
	}
	data, err := io.ReadAll(gz)
	if err != nil {
		return 0, err
	}
	fc, err := geojson.UnmarshalFeatureCollection(data)
	if err != nil {
		return 0, err
	}
	n := 0
	for _, ft := range fc.Features {
		code := ""
		for _, k := range []string{"summitCode", "SummitCode", "code", "ref"} {
			if v, ok := ft.Properties[k].(string); ok && v != "" {
				code = v
				break
			}
		}
		s := byCode[code]
		if s == nil {
			continue
		}
		switch g := ft.Geometry.(type) {
		case orb.Polygon:
			s.zone = append(s.zone, g)
		case orb.MultiPolygon:
			s.zone = append(s.zone, g...)
		default:
			continue
		}
		n++
	}
	return n, nil
}

func write(path string, summits []*summit) error {
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()
	gz := gzip.NewWriter(f)
	w := bufio.NewWriter(gz)
	w.WriteString(`{"type":"FeatureCollection","features":[`)
	for i, s := range summits {
		var g orb.Geometry = s.Pt
		if len(s.zone) > 0 {
			g = orb.Round(s.zone, 1e5)
		}
		ft := geojson.NewFeature(g)
		ft.Properties = geojson.Properties{
			"ref": s.Code, "name": s.Name, "assoc": s.Assoc, "region": s.Region,
			"lat": s.Pt[1], "lon": s.Pt[0], "alt_ft": s.AltFt, "points": s.Points,
			"bonus": s.Bonus, "activations": s.Activations,
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
