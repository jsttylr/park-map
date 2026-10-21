package places

import (
	"fmt"
	"strings"
)

// Summary describes what is at a spot in at most limit characters, for a
// satellite text reply: "2-fer: US-1234 Big Forest, US-0002 Wilderness. Summit
// W7W/KG-001. Near: US-0005 0.3km". Names are dropped, then items, as needed.
func Summary(in []*Place, near []Near, limit int) string {
	var parks, summits []*Place
	for _, p := range in {
		if p.Kind == Summit {
			summits = append(summits, p)
		} else {
			parks = append(parks, p)
		}
	}
	build := func(names bool, keep int) string {
		var b strings.Builder
		item := func(p *Place) string {
			if names && p.Name != "" {
				return p.Ref + " " + p.Name
			}
			return p.Ref
		}
		list := func(ps []*Place) string {
			var s []string
			for i, p := range ps {
				if i == keep {
					s = append(s, fmt.Sprintf("+%d more", len(ps)-keep))
					break
				}
				s = append(s, item(p))
			}
			return strings.Join(s, ", ")
		}
		switch len(parks) {
		case 0:
			b.WriteString("No parks here")
		case 1:
			b.WriteString("1 park: " + list(parks))
		default:
			fmt.Fprintf(&b, "%d-fer: %s", len(parks), list(parks))
		}
		b.WriteString(".")
		if len(summits) > 0 {
			b.WriteString(" Summit " + list(summits) + ".")
		}
		if len(near) > 0 {
			var s []string
			for i, n := range near {
				if i == keep || i == 3 {
					break
				}
				s = append(s, fmt.Sprintf("%s %.1fkm", n.Ref, n.Km))
			}
			b.WriteString(" Near: " + strings.Join(s, ", "))
		}
		return b.String()
	}
	for _, names := range []bool{true, false} {
		for keep := 20; keep >= 1; keep-- {
			if s := build(names, keep); len(s) <= limit {
				return s
			}
		}
	}
	s := build(false, 1)
	if len(s) > limit {
		s = s[:limit]
	}
	return s
}
