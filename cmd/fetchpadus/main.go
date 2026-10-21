// Command fetchpadus downloads PAD-US 4.1 fee, designation and proclamation
// polygons from the USGS PAD-US web service into one gzipped,
// newline-delimited GeoJSON file for builddata.
//
// The national download on ScienceBase sits behind a captcha, so this pages
// through the public feature service instead: a few hundred large requests,
// split by OBJECTID range, slowing down whenever the service says it is busy.
package main

import (
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
	"strconv"
	"sync"
	"time"
)

const (
	service = "https://services.arcgis.com/v01gqwM5QqNysAAi/arcgis/rest/services/Protection_Mechanism_Category_PADUS/FeatureServer/0/query"
	classes = "FeatClass IN ('Fee','Designation','Proclamation')"
	fields  = "OBJECTID,Unit_Nm,Loc_Nm,Mang_Name,Des_Tp,FeatClass,State_Nm"
)

var client = &http.Client{Timeout: 5 * time.Minute}

// get runs one query, waiting and retrying while the service is throttling.
func get(v url.Values) ([]byte, error) {
	var last error
	for attempt := 0; attempt < 10; attempt++ {
		if attempt > 0 {
			wait := time.Duration(min(5*(1<<attempt), 300)) * time.Second
			log.Printf("retry in %v: %v", wait, last)
			time.Sleep(wait)
		}
		resp, err := client.PostForm(service, v)
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
			last = fmt.Errorf("%s", body)
			continue
		}
		return body, nil
	}
	return nil, last
}

func maxObjectID() (int, error) {
	v := url.Values{}
	v.Set("where", classes)
	v.Set("outStatistics", `[{"statisticType":"max","onStatisticField":"OBJECTID","outStatisticFieldName":"m"}]`)
	v.Set("f", "json")
	body, err := get(v)
	if err != nil {
		return 0, err
	}
	var r struct{ Features []struct{ Attributes struct{ M float64 } } }
	if err := json.Unmarshal(body, &r); err != nil || len(r.Features) == 0 {
		return 0, fmt.Errorf("max OBJECTID: %v %s", err, body)
	}
	return int(r.Features[0].Attributes.M), nil
}

// fetchRange returns every feature with lo < OBJECTID <= hi as GeoJSON lines.
func fetchRange(lo, hi, page int, offset float64) ([][]byte, error) {
	var lines [][]byte
	for {
		v := url.Values{}
		v.Set("where", fmt.Sprintf("%s AND OBJECTID > %d AND OBJECTID <= %d", classes, lo, hi))
		v.Set("outFields", fields)
		v.Set("orderByFields", "OBJECTID")
		v.Set("resultRecordCount", strconv.Itoa(page))
		v.Set("outSR", "4326")
		v.Set("maxAllowableOffset", strconv.FormatFloat(offset, 'f', -1, 64))
		v.Set("geometryPrecision", "5")
		v.Set("f", "geojson")
		body, err := get(v)
		if err != nil {
			return nil, err
		}
		var fc struct {
			Features []json.RawMessage `json:"features"`
		}
		if err := json.Unmarshal(body, &fc); err != nil {
			return nil, err
		}
		for _, f := range fc.Features {
			var id struct {
				ID         int `json:"id"`
				Properties struct{ OBJECTID int }
			}
			json.Unmarshal(f, &id)
			lo = max(lo, id.ID, id.Properties.OBJECTID)
			lines = append(lines, f)
		}
		if len(fc.Features) < page {
			return lines, nil
		}
	}
}

func main() {
	out := flag.String("out", "padus.geojsonl.gz", "output file")
	chunk := flag.Int("chunk", 5000, "OBJECTIDs per work unit")
	page := flag.Int("page", 500, "features per request")
	workers := flag.Int("workers", 3, "parallel requests")
	offset := flag.Float64("simplify", 0.0001, "geometry simplification, degrees (~10 m)")
	flag.Parse()

	top, err := maxObjectID()
	if err != nil {
		log.Fatal(err)
	}
	log.Printf("max OBJECTID %d", top)

	f, err := os.Create(*out)
	if err != nil {
		log.Fatal(err)
	}
	gz := gzip.NewWriter(f)
	var mu sync.Mutex
	total, done := 0, 0
	nChunks := (top + *chunk - 1) / *chunk
	jobs := make(chan int)
	var wg sync.WaitGroup
	failed := false
	for i := 0; i < *workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for lo := range jobs {
				lines, err := fetchRange(lo, lo+*chunk, *page, *offset)
				mu.Lock()
				if err != nil {
					log.Printf("OBJECTID %d-%d: %v", lo, lo+*chunk, err)
					failed = true
				}
				for _, l := range lines {
					gz.Write(l)
					gz.Write([]byte{'\n'})
				}
				total += len(lines)
				done++
				log.Printf("chunk %d/%d, %d features so far", done, nChunks, total)
				mu.Unlock()
			}
		}()
	}
	for lo := 0; lo < top; lo += *chunk {
		jobs <- lo
	}
	close(jobs)
	wg.Wait()
	if err := gz.Close(); err != nil {
		log.Fatal(err)
	}
	f.Close()
	if failed {
		log.Fatal("some ranges failed; output is incomplete")
	}
	log.Printf("wrote %d features to %s", total, *out)
}
