// Command parkmap serves a map of POTA parks and SOTA summits with their
// outlines, n-fer filtering, and a lookup of everything at a point.
package main

import (
	"context"
	"embed"
	"encoding/json"
	"flag"
	"fmt"
	"io/fs"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"time"

	"github.com/jsttylr/park-map/internal/gateway"
	"github.com/jsttylr/park-map/internal/places"
	"github.com/paulmach/orb"
	"github.com/paulmach/orb/maptile"
)

//go:embed web
var webFiles embed.FS

func env(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}

func main() {
	dataDir := flag.String("data", env("PARK_MAP_DATA", "data"), "folder with us-parks.geojson.gz and sota.geojson.gz")
	configDir := flag.String("config", env("PARK_MAP_CONFIG", "config"), "folder for settings and the gateway log")
	listen := flag.String("listen", env("PARK_MAP_LISTEN", "0.0.0.0:8075"), "address and port for the web page")
	health := flag.Bool("healthcheck", false, "check that the server is answering, then exit")
	selftest := flag.Bool("selftest", false, "load the data, print what was found, then exit")
	explain := flag.String("explain", "", "with -selftest: comma-separated references to describe in detail")
	regionsOut := flag.String("write-regions", "", "work out the n-fer overlap patches, save them to this file, then exit")
	flag.Parse()

	if *health {
		_, port, _ := cutLast(*listen, ":")
		resp, err := http.Get("http://127.0.0.1:" + port + "/api/info")
		if err != nil || resp.StatusCode != 200 {
			os.Exit(1)
		}
		return
	}

	start := time.Now()
	set, err := places.Load(filepath.Join(*dataDir, "us-parks.geojson.gz"), filepath.Join(*dataDir, "sota.geojson.gz"))
	if err != nil {
		log.Fatal(err)
	}
	log.Printf("loaded %d places in %v", len(set.Places), time.Since(start).Round(time.Millisecond))
	t := time.Now()
	set.ComputeOverlaps()
	log.Printf("worked out overlaps in %v", time.Since(t).Round(time.Millisecond))
	regionsFile := filepath.Join(*dataDir, "nfer.geojson.gz")
	if *regionsOut != "" {
		set.ComputeRegions()
		if err := set.WriteRegions(*regionsOut); err != nil {
			log.Fatal(err)
		}
		log.Printf("saved %d overlap patches to %s", len(set.Regions.Places), *regionsOut)
		return
	}
	if err := set.LoadRegions(regionsFile); err != nil {
		log.Printf("no saved overlap patches (%v), working them out", err)
		set.ComputeRegions()
	} else {
		log.Printf("loaded %d overlap patches", len(set.Regions.Places))
	}
	t = time.Now()
	set.PrepareTiles()
	log.Printf("prepared map detail levels in %v", time.Since(t).Round(time.Millisecond))
	info := stats(set)
	var ms runtime.MemStats
	runtime.GC()
	runtime.ReadMemStats(&ms)
	log.Printf("%+v, memory in use %d MB", info, ms.HeapAlloc>>20)

	if *selftest {
		runSelftest(set, *explain)
		return
	}

	srv := &server{set: set, info: info, cache: &places.TileCache{Max: 20000}}
	mux := http.NewServeMux()
	web, _ := fs.Sub(webFiles, "web")
	mux.Handle("GET /", http.FileServerFS(web))
	mux.HandleFunc("GET /tiles/{z}/{x}/{y}", srv.tile)
	mux.HandleFunc("GET /api/at", srv.at)
	mux.HandleFunc("GET /api/search", srv.search)
	mux.HandleFunc("GET /api/info", func(w http.ResponseWriter, r *http.Request) { writeJSON(w, srv.info) })
	store, err := gateway.OpenStore(*configDir)
	if err != nil {
		log.Fatalf("reading settings: %v", err)
	}
	gw := gateway.New(set, store, gateway.NewLog(filepath.Join(*configDir, "gateway.log")))
	settingsPage, _ := webFiles.ReadFile("web/settings.html")
	warn := configWarning(*configDir)
	if warn != "" {
		log.Print(warn)
		gw.Log().Warn("Settings won't survive an update", warn)
	}
	gatewayRoutes(mux, gw, os.Getenv("PARK_MAP_ADMIN_PASSWORD"), warn, settingsPage)
	go gw.Run(context.Background())
	log.Printf("ready after %v: open http://%s/", time.Since(start).Round(time.Second), *listen)
	log.Fatal(http.ListenAndServe(*listen, mux))
}

func cutLast(s, sep string) (string, string, bool) {
	for i := len(s) - len(sep); i >= 0; i-- {
		if s[i:i+len(sep)] == sep {
			return s[:i], s[i+len(sep):], true
		}
	}
	return s, "", false
}

type info struct {
	Parks    int            `json:"parks"`
	Outlines int            `json:"outlines"`
	Summits  int            `json:"summits"`
	Zones    int            `json:"zones"`
	NFer     map[string]int `json:"nfer"` // parks by n-fer count
}

func stats(s *places.Set) info {
	in := info{NFer: map[string]int{}}
	for _, p := range s.Places {
		if p.Kind == places.Summit {
			in.Summits++
			if p.Shape != nil {
				in.Zones++
			}
			continue
		}
		in.Parks++
		if p.Shape != nil {
			in.Outlines++
		}
		in.NFer[strconv.Itoa(min(p.NFer, 5))]++
	}
	return in
}

type server struct {
	set   *places.Set
	info  info
	cache *places.TileCache
}

func (s *server) tile(w http.ResponseWriter, r *http.Request) {
	z, err1 := strconv.Atoi(r.PathValue("z"))
	x, err2 := strconv.Atoi(r.PathValue("x"))
	yStr := r.PathValue("y")
	if ext := filepath.Ext(yStr); ext != "" {
		yStr = yStr[:len(yStr)-len(ext)]
	}
	y, err3 := strconv.Atoi(yStr)
	t := maptile.New(uint32(x), uint32(y), maptile.Zoom(z))
	if err1 != nil || err2 != nil || err3 != nil || z < 0 || z > 18 || x < 0 || y < 0 || !t.Valid() {
		http.Error(w, "bad tile", http.StatusBadRequest)
		return
	}
	b, err := s.cache.Get(s.set, t)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/vnd.mapbox-vector-tile")
	w.Header().Set("Content-Encoding", "gzip")
	w.Header().Set("Cache-Control", "public, max-age=3600")
	w.Write(b)
}

type placeJSON struct {
	Kind   string      `json:"kind"`
	Ref    string      `json:"ref"`
	Name   string      `json:"name"`
	Loc    string      `json:"loc"`
	Lat    float64     `json:"lat"`
	Lon    float64     `json:"lon"`
	NFer   int         `json:"nfer"`
	AltFt  any         `json:"alt_ft,omitempty"`
	Points any         `json:"points,omitempty"`
	Km     *float64    `json:"km,omitempty"`
	BBox   *[4]float64 `json:"bbox,omitempty"`
}

func toJSON(p *places.Place) placeJSON {
	j := placeJSON{Kind: p.Kind.String(), Ref: p.Ref, Name: p.Name, Loc: p.Loc, Lat: p.Pt[1], Lon: p.Pt[0], NFer: p.NFer}
	if p.Kind == places.Summit {
		j.AltFt, j.Points = p.Props["alt_ft"], p.Props["points"]
	}
	if p.Shape != nil {
		b := p.Shape.Bound
		j.BBox = &[4]float64{b.Min[0], b.Min[1], b.Max[0], b.Max[1]}
	}
	return j
}

func (s *server) at(w http.ResponseWriter, r *http.Request) {
	lat, err1 := strconv.ParseFloat(r.URL.Query().Get("lat"), 64)
	lon, err2 := strconv.ParseFloat(r.URL.Query().Get("lon"), 64)
	if err1 != nil || err2 != nil || lat < -90 || lat > 90 || lon < -180 || lon > 180 {
		http.Error(w, "lat and lon are required", http.StatusBadRequest)
		return
	}
	in, near := s.set.At(orb.Point{lon, lat}, 2)
	text := places.Summary(in, near, 160)
	if r.URL.Query().Get("format") == "text" {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		fmt.Fprintln(w, text)
		return
	}
	out := struct {
		Lat  float64     `json:"lat"`
		Lon  float64     `json:"lon"`
		Here []placeJSON `json:"here"`
		Near []placeJSON `json:"near"`
		Text string      `json:"text"`
	}{Lat: lat, Lon: lon, Here: []placeJSON{}, Near: []placeJSON{}, Text: text}
	for _, p := range in {
		out.Here = append(out.Here, toJSON(p))
	}
	for _, n := range near {
		j := toJSON(n.Place)
		km := n.Km
		j.Km = &km
		out.Near = append(out.Near, j)
	}
	writeJSON(w, out)
}

func (s *server) search(w http.ResponseWriter, r *http.Request) {
	out := []placeJSON{}
	for _, p := range s.set.Search(r.URL.Query().Get("q"), 20) {
		out = append(out, toJSON(p))
	}
	writeJSON(w, out)
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(v)
}
