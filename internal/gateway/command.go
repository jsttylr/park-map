package gateway

import (
	"fmt"
	"regexp"
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

const helpText = "?=what's here. spot 14062 [mode] [note]=all parks+summit here. pota 14062=parks here. sota 7032=summit here. Or name it: pota US-1234 14062"

var modes = map[string]string{
	"CW": "CW", "SSB": "SSB", "USB": "SSB", "LSB": "SSB", "FM": "FM", "AM": "AM",
	"FT8": "FT8", "FT4": "FT4", "DATA": "DATA", "DIGI": "DATA", "RTTY": "RTTY", "PSK": "PSK31", "PSK31": "PSK31",
}

var (
	potaRefRe = regexp.MustCompile(`^[A-Z0-9]{1,4}-\d{4,5}$`)
	sotaRefRe = regexp.MustCompile(`^[A-Z0-9]{1,4}/[A-Z0-9]{2}-\d{3}$`)
)

// ParseCommand reads the typed text. pin, if set, is dropped from the words.
//
//	spot <kHz> [mode] [comment]   every park and summit at the position
//	pota [refs] <kHz> ...         the parks named, or the parks at the position
//	sota [ref] <kHz> ...          the summit named, or the summit at the position
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
		// pota and sota may name references first; without them they use
		// the position, like spot.
		if c.Kind != "spot" && len(rest) > 0 && !isNumber(rest[0]) {
			re, example := potaRefRe, "US-1234"
			if c.Kind == "sota" {
				re, example = sotaRefRe, "W4V/BR-001"
			}
			for _, r := range strings.Split(rest[0], ",") {
				if r = strings.ToUpper(strings.TrimSpace(r)); r == "" {
					continue
				} else if !re.MatchString(r) {
					c.Err = "Can't read reference " + r + ". Use e.g. " + c.Kind + " " + example + " 14062"
					return c
				}
				c.Refs = append(c.Refs, r)
			}
			rest = rest[1:]
		}
		if len(rest) == 0 {
			c.Err = "Give a frequency in kHz, e.g. " + v + " 14062"
			return c
		}
		khz, err := parseFreq(rest[0])
		if err != "" {
			c.Err = err
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

func isNumber(s string) bool {
	_, err := strconv.ParseFloat(s, 64)
	return err == nil
}

// bands are the US amateur allocations in kHz. A frequency outside them is
// almost always a typo, so it is refused rather than spotted.
var bands = [][2]float64{
	{135.7, 137.8}, {472, 479}, {1800, 2000}, {3500, 4000}, {5330, 5407}, {7000, 7300},
	{10100, 10150}, {14000, 14350}, {18068, 18168}, {21000, 21450}, {24890, 24990},
	{28000, 29700}, {50000, 54000}, {144000, 148000}, {222000, 225000}, {420000, 450000},
}

// parseFreq reads a frequency in kHz ("14062" or "14062.5"). MHz and
// anything outside the ham bands are refused with a message saying why.
func parseFreq(s string) (float64, string) {
	f, err := strconv.ParseFloat(s, 64)
	if err != nil || f <= 0 {
		return 0, "Can't read frequency " + s + ". Use kHz, e.g. 14062"
	}
	if f < 1000 && strings.Contains(s, ".") {
		return 0, s + " looks like MHz. Use kHz, e.g. " + strconv.FormatFloat(f*1000, 'f', -1, 64)
	}
	for _, b := range bands {
		if f >= b[0] && f <= b[1] {
			return f, ""
		}
	}
	return 0, s + " kHz is not in a ham band. Use kHz, e.g. 14062"
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
