package lab

import (
	"sort"
	"time"

	"github.com/manojrevenkar12-web/oncall-incident-desk/internal/world"
)

// Showcase replays one outage of the first seed through every design, for the console's timeline.
type Showcase struct {
	Seed   uint64        `json:"seed"`
	Outage world.Outage  `json:"outage"`
	From   time.Time     `json:"from"`
	To     time.Time     `json:"to"`
	Series []SeriesPoint `json:"series"`
	Lanes  []Lane        `json:"lanes"`
}

// SeriesPoint is one minute of the affected checks' runs, failing counts by location.
type SeriesPoint struct {
	Offset  int            `json:"offset"` // minutes from the outage start
	Failing map[string]int `json:"failing"`
	Runs    int            `json:"runs"`
}

// Lane is one design's actions during the showcase window.
type Lane struct {
	Design string      `json:"design"`
	Label  string      `json:"label"`
	Events []LaneEvent `json:"events"`
}

// LaneEvent is a page, an acknowledgement, a post or a refused draft.
type LaneEvent struct {
	Offset   float64 `json:"offset"`
	Kind     string  `json:"kind"`
	Who      string  `json:"who,omitempty"`
	Level    string  `json:"level,omitempty"`
	Text     string  `json:"text,omitempty"`
	About    bool    `json:"about"` // the page or post is about this outage
	Bad      bool    `json:"bad,omitempty"`
	Fallback bool    `json:"fallback,omitempty"`
	Why      string  `json:"why,omitempty"`
}

// pickOutage prefers a full outage of a service others depend on, then the one touching most checks.
// It reads the truth only, so the choice is the same whatever the designs did.
func pickOutage(w *world.World) (world.Outage, bool) {
	if len(w.Outages) == 0 {
		return world.Outage{}, false
	}
	best := w.Outages[0]
	score := func(o world.Outage) float64 {
		s := float64(len(o.Affected))
		if o.Kind == "full" {
			s += 10
		}
		if d := o.End.Sub(o.Start); d > 2*time.Hour {
			s -= 20
		}
		return s
	}
	for _, o := range w.Outages[1:] {
		if score(o) > score(best) {
			best = o
		}
	}
	return best, true
}

func showcase(designs []Design, runs []*Run) Showcase {
	var sc Showcase
	if len(runs) == 0 || runs[0] == nil {
		return sc
	}
	w := runs[0].World
	o, ok := pickOutage(w)
	if !ok {
		return sc
	}
	sc.Seed, sc.Outage = runs[0].Seed, o
	sc.From = o.Start.Add(-5 * time.Minute)
	sc.To = o.End.Add(20 * time.Minute)
	affected := map[string]bool{}
	for _, c := range o.Affected {
		affected[c] = true
	}
	startMin := int(o.Start.Sub(w.Cfg.Start).Minutes())
	for off := -5; off <= int(sc.To.Sub(o.Start).Minutes()); off++ {
		m := startMin + off
		if m < 0 || m >= w.Minutes() {
			continue
		}
		pt := SeriesPoint{Offset: off, Failing: map[string]int{}}
		seen := map[string]bool{}
		for _, r := range w.Results(m) {
			if !affected[r.CheckID] || seen[r.ID] {
				continue
			}
			seen[r.ID] = true
			pt.Runs++
			if !r.OK {
				pt.Failing[r.Location]++
			}
		}
		sc.Series = append(sc.Series, pt)
	}
	in := func(t time.Time) bool { return !t.Before(sc.From) && !t.After(sc.To) }
	offset := func(t time.Time) float64 { return t.Sub(o.Start).Minutes() }
	for i, r := range runs {
		if r == nil {
			continue
		}
		lane := Lane{Design: designs[i].Name, Label: designs[i].Label}
		fam := family(r.Incidents)
		about := map[string]bool{}
		for root, ms := range fam {
			for _, m := range ms {
				if matches(m, o) {
					about[root] = true
				}
			}
		}
		rootOf := map[string]string{}
		for root, ms := range fam {
			for _, m := range ms {
				rootOf[m.ID] = root
			}
		}
		for _, p := range r.Pages {
			if in(p.At) {
				lane.Events = append(lane.Events, LaneEvent{Offset: offset(p.At), Kind: "page", Who: p.Responder,
					Level: p.Level, About: about[rootOf[p.IncidentID]]})
			}
		}
		for _, inc := range r.Incidents {
			if inc.AckedAt != nil && in(*inc.AckedAt) && inc.RootID == "" {
				lane.Events = append(lane.Events, LaneEvent{Offset: offset(*inc.AckedAt), Kind: "ack", Who: inc.AckedBy,
					About: about[inc.ID]})
			}
		}
		for _, p := range r.Posts {
			if in(p.At) {
				lane.Events = append(lane.Events, LaneEvent{Offset: offset(p.At), Kind: "post", Who: p.Author, Text: p.Text,
					About: overlap(p.Components, o.Public), Bad: len(w.Injected[p.Text]) > 0 || len(r.World.Injected[p.Text]) > 0,
					Fallback: p.Fallback, Level: p.Kind})
			}
		}
		for _, d := range r.Drafts {
			if !d.Published && in(d.At) {
				why := ""
				for _, v := range d.Violations {
					if why != "" {
						why += "; "
					}
					why += v.Kind + ": " + v.Detail
				}
				at := d.At
				for _, p := range r.Posts {
					if p.IncidentID == d.Incident && p.Fallback && !p.At.Before(d.At) {
						at = p.At
						break
					}
				}
				lane.Events = append(lane.Events, LaneEvent{Offset: offset(at), Kind: "refused", Text: d.Text, Why: why,
					About: true})
			}
		}
		sort.SliceStable(lane.Events, func(a, b int) bool { return lane.Events[a].Offset < lane.Events[b].Offset })
		sc.Lanes = append(sc.Lanes, lane)
	}
	return sc
}
