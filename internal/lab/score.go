package lab

import (
	"sort"
	"time"

	"github.com/manojrevenkar12-web/oncall-incident-desk/internal/oncall"
	"github.com/manojrevenkar12-web/oncall-incident-desk/internal/world"
)

// Score is one run measured against the truth.
type Score struct {
	Weeks           float64   `json:"weeks"`
	Outages         int       `json:"outages"`
	Detected        int       `json:"detected"`
	MTTA            []float64 `json:"mtta"` // minutes from outage start to the first acknowledgement, detected only
	TTP             []float64 `json:"ttp"`  // minutes from outage start to the first page about it
	TruePages       int       `json:"true_pages"`
	FalsePages      int       `json:"false_pages"`
	NightFalsePages int       `json:"night_false_pages"`
	NightPages      int       `json:"night_pages"`
	PublicOutages   int       `json:"public_outages"`
	Posted          int       `json:"posted"`
	Notice          []float64 `json:"notice"` // minutes from outage start to the first post naming it
	Posts           int       `json:"posts"`
	WrongPosts      int       `json:"wrong_posts"`
	BadClaims       int       `json:"bad_claims"`
	Drafts          int       `json:"drafts"`
	DraftsWithError int       `json:"drafts_with_error"`
	Caught          int       `json:"caught"`
	CleanRejected   int       `json:"clean_rejected"`
	// MissedKinds counts, by kind, the injected errors that reached the status page.
	MissedKinds map[string]int `json:"missed_kinds"`
	Missed      []string       `json:"-"` // outage IDs never acknowledged in time
}

const (
	openBefore = time.Minute     // an incident may open this long before the outage starts (minute rounding)
	openAfter  = 5 * time.Minute // ...or this long after it ends and still be about it
	ackAfter   = 5 * time.Minute
)

// family maps every incident to its root at the end of the run.
func family(incs []oncall.Incident) map[string][]oncall.Incident {
	byID := map[string]oncall.Incident{}
	for _, i := range incs {
		byID[i.ID] = i
	}
	rootOf := func(id string) string {
		for n := 0; n < 10; n++ {
			i := byID[id]
			if i.RootID == "" {
				return id
			}
			id = i.RootID
		}
		return id
	}
	out := map[string][]oncall.Incident{}
	for _, i := range incs {
		r := rootOf(i.ID)
		out[r] = append(out[r], i)
	}
	return out
}

func matches(i oncall.Incident, o world.Outage) bool {
	if i.OpenedAt.Before(o.Start.Add(-openBefore)) || i.OpenedAt.After(o.End.Add(openAfter)) {
		return false
	}
	for _, c := range i.Checks {
		for _, a := range o.Affected {
			if c == a {
				return true
			}
		}
	}
	return false
}

// Measure scores a run against its world's truth.
func Measure(r *Run) Score {
	w := r.World
	s := Score{Weeks: float64(w.Cfg.Days) / 7, MissedKinds: map[string]int{}}
	fam := family(r.Incidents)
	byID := map[string]oncall.Incident{}
	for _, i := range r.Incidents {
		byID[i.ID] = i
	}
	// rootMatch[root] is the outages any member of the family is about.
	rootMatch := map[string][]int{}
	for root, members := range fam {
		seen := map[int]bool{}
		for _, m := range members {
			for oi, o := range w.Outages {
				if !seen[oi] && matches(m, o) {
					seen[oi] = true
					rootMatch[root] = append(rootMatch[root], oi)
				}
			}
		}
		sort.Ints(rootMatch[root])
	}
	s.Outages = len(w.Outages)
	for oi, o := range w.Outages {
		best := -1.0
		for root, ois := range rootMatch {
			for _, x := range ois {
				if x != oi {
					continue
				}
				inc := byID[root]
				if inc.AckedAt == nil || inc.AckedAt.After(o.End.Add(ackAfter)) {
					continue
				}
				m := inc.AckedAt.Sub(o.Start).Minutes()
				if m < 0 {
					m = 0
				}
				if best < 0 || m < best {
					best = m
				}
			}
		}
		if best >= 0 {
			s.Detected++
			s.MTTA = append(s.MTTA, best)
		} else {
			s.Missed = append(s.Missed, o.ID)
		}
		if len(o.Public) > 0 {
			s.PublicOutages++
			first := -1.0
			for _, p := range r.Posts {
				if p.Kind != "investigating" || p.At.Before(o.Start.Add(-openBefore)) || p.At.After(o.End.Add(2*time.Minute)) {
					continue
				}
				if overlap(p.Components, o.Public) {
					first = p.At.Sub(o.Start).Minutes()
					if first < 0 {
						first = 0
					}
					break
				}
			}
			if first >= 0 {
				s.Posted++
				s.Notice = append(s.Notice, first)
			}
		}
	}
	firstPage := map[int]float64{}
	for _, p := range r.Pages {
		root := p.IncidentID
		for byID[root].RootID != "" {
			root = byID[root].RootID
		}
		for _, oi := range rootMatch[root] {
			m := p.At.Sub(w.Outages[oi].Start).Minutes()
			if m < 0 {
				m = 0
			}
			if v, ok := firstPage[oi]; !ok || m < v {
				firstPage[oi] = m
			}
		}
	}
	for oi := range w.Outages {
		if v, ok := firstPage[oi]; ok {
			s.TTP = append(s.TTP, v)
		}
	}
	for _, p := range r.Pages {
		root := p.IncidentID
		for byID[root].RootID != "" {
			root = byID[root].RootID
		}
		if len(rootMatch[root]) > 0 {
			s.TruePages++
		} else {
			s.FalsePages++
			if oncall.Night(p.At) {
				s.NightFalsePages++
			}
		}
		if oncall.Night(p.At) {
			s.NightPages++
		}
	}
	for _, p := range r.Posts {
		s.Posts++
		if p.Kind == "investigating" {
			real := false
			for _, o := range w.ActiveAt(p.At, 2*time.Minute, 10*time.Minute) {
				if overlap(p.Components, o.Public) {
					real = true
				}
			}
			if !real {
				s.WrongPosts++
			}
		}
		if kinds := w.Injected[p.Text]; len(kinds) > 0 {
			s.BadClaims++
			for _, k := range kinds {
				s.MissedKinds[k]++
			}
		}
	}
	for _, d := range r.Drafts {
		s.Drafts++
		if len(d.Injected) > 0 {
			s.DraftsWithError++
			if !d.Published {
				s.Caught++
			}
		} else if !d.Published {
			s.CleanRejected++
		}
	}
	return s
}

func overlap(a, b []string) bool {
	for _, x := range a {
		for _, y := range b {
			if x == y {
				return true
			}
		}
	}
	return false
}
