package gateway

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/jsttylr/park-map/internal/places"
	"github.com/paulmach/orb"
)

// Command is what a message asks for.
type Command struct {
	Kind    string   // lookup, help, spot, pota, sota
	Refs    []string // for pota and sota
	KHz     float64
	Mode    string
	Comment string
	Err     string
}

const helpText = "? = parks+summits here. spot 14.062 cw [comment] = spot all here. pota US-1234 14.062 [mode]. sota W4V/BR-001 7.032 [mode]"

var modes = map[string]string{
	"CW": "CW", "SSB": "SSB", "USB": "SSB", "LSB": "SSB", "FM": "FM", "AM": "AM",
	"FT8": "FT8", "FT4": "FT4", "DATA": "DATA", "DIGI": "DATA", "RTTY": "RTTY", "PSK": "PSK31", "PSK31": "PSK31",
}

// ParseCommand reads the typed text. pin, if set, is dropped from the words.
func ParseCommand(text, pin string) Command {
	var words []string
	for _, w := range strings.Fields(text) {
		if pin == "" || w != pin {
			words = append(words, w)
		}
	}
	if len(words) == 0 {
		return Command{Kind: "lookup"}
	}
	switch v := strings.ToLower(words[0]); v {
	case "?", "where", "here", "lookup", "parks":
		return Command{Kind: "lookup"}
	case "help", "h":
		return Command{Kind: "help"}
	case "spot", "s", "pota", "p", "sota":
		c := Command{Kind: map[string]string{"s": "spot", "p": "pota"}[v]}
		if c.Kind == "" {
			c.Kind = v
		}
		rest := words[1:]
		if c.Kind != "spot" {
			if len(rest) == 0 {
				c.Err = "Give a reference and frequency, e.g. " + v + " " + map[string]string{"pota": "US-1234", "sota": "W4V/BR-001"}[c.Kind] + " 14.062"
				return c
			}
			for _, r := range strings.Split(rest[0], ",") {
				if r = strings.ToUpper(strings.TrimSpace(r)); r != "" {
					c.Refs = append(c.Refs, r)
				}
			}
			rest = rest[1:]
		}
		if len(rest) == 0 {
			c.Err = "Give a frequency, e.g. 14.062"
			return c
		}
		khz, ok := parseFreq(rest[0])
		if !ok {
			c.Err = "Can't read frequency " + rest[0]
			return c
		}
		c.KHz, rest = khz, rest[1:]
		if len(rest) > 0 {
			if m, ok := modes[strings.ToUpper(rest[0])]; ok {
				c.Mode, rest = m, rest[1:]
			}
		}
		if c.Mode == "" {
			c.Mode = guessMode(khz)
		}
		c.Comment = strings.TrimLeft(strings.Join(rest, " "), "'\"-: ")
		return c
	}
	// Anything else, such as bare coordinates, is a lookup.
	if _, _, ok := findPair(text); ok {
		return Command{Kind: "lookup"}
	}
	return Command{Kind: "help", Err: "Unknown command."}
}

// parseFreq reads MHz ("14.062") or kHz ("14062") and returns kHz.
func parseFreq(s string) (float64, bool) {
	f, err := strconv.ParseFloat(strings.ReplaceAll(strings.TrimSuffix(strings.ToLower(s), "mhz"), ",", "."), 64)
	if err != nil || f <= 0 {
		return 0, false
	}
	if f < 1000 {
		f *= 1000
	}
	if f < 1800 || f > 450000 {
		return 0, false
	}
	return f, true
}

// guessMode follows the US band plan roughly: the bottom of each band is CW.
func guessMode(khz float64) string {
	cw := [][2]float64{{1800, 1840}, {3500, 3600}, {7000, 7125}, {10100, 10150}, {14000, 14150},
		{18068, 18110}, {21000, 21200}, {24890, 24930}, {28000, 28300}, {50000, 50100}, {144000, 144100}}
	for _, r := range cw {
		if khz >= r[0] && khz < r[1] {
			return "CW"
		}
	}
	if khz >= 144000 {
		return "FM"
	}
	return "SSB"
}

func fmtMHz(khz float64) string {
	return strconv.FormatFloat(khz/1000, 'f', -1, 64)
}

func fmtKHz(khz float64) string {
	return strconv.FormatFloat(khz, 'f', -1, 64)
}

// here returns the parks and summits whose outline or zone contains pt.
func here(set *places.Set, lat, lon float64) (parks, summits []string, in []*places.Place, near []places.Near) {
	in, near = set.At(orb.Point{lon, lat}, 2)
	for _, p := range in {
		if p.Kind == places.Summit {
			summits = append(summits, p.Ref)
		} else {
			parks = append(parks, p.Ref)
		}
	}
	return
}

func clip(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return strings.TrimSpace(s[:n-2]) + "++"
}

func posText(lat, lon float64) string { return fmt.Sprintf("%.5f,%.5f", lat, lon) }
