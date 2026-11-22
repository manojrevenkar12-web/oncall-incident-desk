// Package world is the simulated customer: a fleet of services checked from several locations, the outages that
// really happen to it (the truth), the noise that is not an outage (a monitoring location's network blips,
// flaky checks, duplicated webhook deliveries), the people who carry the pager, and a stand-in for a language
// model with stated error rates.
//
// The world reads only config/world.json (or config/heldout.json) and imports only the vocabulary. Every draw
// is keyed by the seed and what it is about, never by call order, so every design sees the same luck.
package world

import (
	"encoding/json"
	"fmt"
	"hash/fnv"
	"math"
	"os"
	"sort"
	"strconv"
	"time"

	"github.com/manojrevenkar12-web/oncall-incident-desk/internal/oncall"
)

// Config is the world's own description. The desk's settings are not in it and the world never reads them.
type Config struct {
	Name       string             `json:"name"`
	Seed       uint64             `json:"seed"`
	Days       int                `json:"days"`
	Start      time.Time          `json:"start"`
	Locations  []string           `json:"locations"`
	Teams      []oncall.Team      `json:"teams"`
	Components []oncall.Component `json:"components"`
	Services   []ServiceSpec      `json:"services"`
	Outages    OutageSpec         `json:"outages"`
	Blips      BlipSpec           `json:"blips"`
	Flakes     FlakeSpec          `json:"flakes"`
	Delivery   DeliverySpec       `json:"delivery"`
	People     PeopleSpec         `json:"people"`
	Model      ModelSpec          `json:"model"`
}

// ServiceSpec is one service, its checks and how often it really breaks.
type ServiceSpec struct {
	ID          string         `json:"id"`
	Team        string         `json:"team"`
	DependsOn   []string       `json:"depends_on,omitempty"`
	Hosts       []string       `json:"hosts,omitempty"`
	RatePerWeek float64        `json:"rate_per_week"`
	Checks      []oncall.Check `json:"checks"`
}

// Kind is one way a service breaks.
type Kind struct {
	Weight    float64 `json:"weight"`
	Fail      float64 `json:"fail"`       // chance a run fails while it lasts
	MedianMin float64 `json:"median_min"` // lognormal duration
	Sigma     float64 `json:"sigma"`
}

// OutageSpec says how outages look.
type OutageSpec struct {
	Kinds       map[string]Kind `json:"kinds"` // full, partial, regional, degraded
	MinMin      float64         `json:"min_min"`
	MaxMin      float64         `json:"max_min"`
	CascadeFail float64         `json:"cascade_fail"` // dependants' runs failing during a full outage
}

// BlipSpec is a monitoring location losing its network: every check there fails, no customer notices.
type BlipSpec struct {
	PerLocationPerWeek float64 `json:"per_location_per_week"`
	MinMin             int     `json:"min_min"`
	MaxMin             int     `json:"max_min"`
	Fail               float64 `json:"fail"`
}

// FlakeSpec is checks failing once for no reason a customer would see.
type FlakeSpec struct {
	Base       float64 `json:"base"`
	FlakyShare float64 `json:"flaky_share"`
	Flaky      float64 `json:"flaky"`
}

// DeliverySpec is how the webhook delivers results.
type DeliverySpec struct {
	Duplicate float64 `json:"duplicate"`
}

// PeopleSpec is how responders answer pages and write status posts.
type PeopleSpec struct {
	MissDay        float64 `json:"miss_day"`
	MissNight      float64 `json:"miss_night"`
	FatiguePerPage float64 `json:"fatigue_per_page"` // extra miss chance per page received in the last 24 h
	FatigueCap     float64 `json:"fatigue_cap"`
	AckDayMin      float64 `json:"ack_day_min"`
	AckNightMin    float64 `json:"ack_night_min"`
	AckSigma       float64 `json:"ack_sigma"`
	PostChance     float64 `json:"post_chance"`
	PostMin        float64 `json:"post_min"`
	PostSigma      float64 `json:"post_sigma"`
}

// ModelSpec is the stand-in language model: how its paging judgement and its drafts go wrong.
type ModelSpec struct {
	DownTwoPlus    float64            `json:"down_two_plus"`   // per minute, when two or more locations fail
	DownOne        float64            `json:"down_one"`        // per minute, when one location fails
	UpAllPass      float64            `json:"up_all_pass"`     // per minute, when every location passes
	Errors         map[string]float64 `json:"errors"`          // chance per draft of each error kind
	PhrasesFile    string             `json:"phrases_file"`    // the phrase bank the model writes from
	HeldoutPhrases bool               `json:"heldout_phrases"` // informational
}

// Outage is the truth about one real failure.
type Outage struct {
	ID       string    `json:"id"`
	Service  string    `json:"service"`
	Kind     string    `json:"kind"`
	Start    time.Time `json:"start"`
	End      time.Time `json:"end"`
	Location string    `json:"location,omitempty"` // regional
	Check    string    `json:"check,omitempty"`    // partial
	// Affected is every check that fails because of it, dependants included; Public the public components.
	Affected []string `json:"affected"`
	Public   []string `json:"public"`
	fail     float64
}

// Blip is the truth about one monitoring-location network problem.
type Blip struct {
	Location string    `json:"location"`
	Start    time.Time `json:"start"`
	End      time.Time `json:"end"`
}

// World is one seeded run of the configuration.
type World struct {
	Cfg      Config
	Outages  []Outage
	Blips    []Blip
	cascade  map[string][]string // service → dependants, transitive, sorted
	checks   []oncall.Check
	checkOf  map[string]oncall.Check
	svcOf    map[string]ServiceSpec
	flaky    map[string]float64
	bases    map[string]uint64
	phrases  Phrases
	received map[string][]time.Time // pages a responder got, for fatigue
	// Injected records, per draft text, the errors the model put into it: the lab's truth for the checker.
	Injected map[string][]string
}

// Load reads a world configuration.
func Load(path string) (Config, error) {
	var c Config
	b, err := os.ReadFile(path)
	if err != nil {
		return c, err
	}
	if err := json.Unmarshal(b, &c); err != nil {
		return c, fmt.Errorf("%s: %w", path, err)
	}
	return c, nil
}

// New draws a world from its configuration and a seed.
func New(c Config, seed uint64, phrases Phrases) *World {
	c.Seed = seed
	w := &World{Cfg: c, checkOf: map[string]oncall.Check{}, svcOf: map[string]ServiceSpec{}, flaky: map[string]float64{},
		received: map[string][]time.Time{}, bases: map[string]uint64{}, Injected: map[string][]string{}, phrases: phrases}
	for _, s := range c.Services {
		w.svcOf[s.ID] = s
		for _, ch := range s.Checks {
			ch.Service = s.ID
			if len(ch.Locations) == 0 {
				ch.Locations = c.Locations
			}
			w.checks = append(w.checks, ch)
			w.checkOf[ch.ID] = ch
			p := c.Flakes.Base
			if w.u("flaky", ch.ID) < c.Flakes.FlakyShare {
				p = c.Flakes.Flaky
			}
			w.flaky[ch.ID] = p
		}
	}
	sort.Slice(w.checks, func(i, j int) bool { return w.checks[i].ID < w.checks[j].ID })
	w.cascade = map[string][]string{}
	for _, s := range c.Services {
		w.cascade[s.ID] = w.dependants(s.ID)
	}
	w.drawOutages()
	w.drawBlips()
	return w
}

func (w *World) dependants(id string) []string {
	seen := map[string]bool{}
	var walk func(string)
	walk = func(x string) {
		for _, s := range w.Cfg.Services {
			for _, d := range s.DependsOn {
				if d == x && !seen[s.ID] {
					seen[s.ID] = true
					walk(s.ID)
				}
			}
		}
	}
	walk(id)
	out := make([]string, 0, len(seen))
	for s := range seen {
		out = append(out, s)
	}
	sort.Strings(out)
	return out
}

// Catalog is the customer's monitoring-as-code configuration, handed to the desk.
func (w *World) Catalog() oncall.Catalog {
	c := oncall.Catalog{Components: w.Cfg.Components, Teams: w.Cfg.Teams, RotationStart: w.Cfg.Start}
	for _, s := range w.Cfg.Services {
		c.Services = append(c.Services, oncall.Service{ID: s.ID, Team: s.Team, DependsOn: s.DependsOn, Hosts: s.Hosts})
	}
	c.Checks = append(c.Checks, w.checks...)
	return c
}

// End is when the world stops.
func (w *World) End() time.Time { return w.Cfg.Start.Add(time.Duration(w.Cfg.Days) * 24 * time.Hour) }

// u is a uniform draw keyed by the seed and the parts.
func (w *World) u(parts ...any) float64 {
	h := fnv.New64a()
	fmt.Fprint(h, w.Cfg.Seed)
	for _, p := range parts {
		fmt.Fprint(h, "|", p)
	}
	return unit(h.Sum64())
}

// base is the keyed hash of one check at one location; mix adds the minute. Results are drawn from these
// directly because they are the hot path.
func (w *World) base(check, loc string) uint64 {
	k := check + "|" + loc
	if b, ok := w.bases[k]; ok {
		return b
	}
	h := fnv.New64a()
	fmt.Fprint(h, w.Cfg.Seed, "|run|", k)
	b := h.Sum64()
	w.bases[k] = b
	return b
}

func mix(b, m uint64) uint64 {
	x := b ^ (m * 0x9E3779B97F4A7C15)
	x = (x ^ (x >> 33)) * 0xFF51AFD7ED558CCD
	x = (x ^ (x >> 33)) * 0xC4CEB9FE1A85EC53
	return x ^ (x >> 33)
}

func unit(x uint64) float64 {
	x += 0x9E3779B97F4A7C15
	x = (x ^ (x >> 30)) * 0xBF58476D1CE4E5B9
	x = (x ^ (x >> 27)) * 0x94D049BB133111EB
	x ^= x >> 31
	return float64(x>>11) / float64(1<<53)
}

func normal(u1, u2 float64) float64 {
	if u1 < 1e-12 {
		u1 = 1e-12
	}
	return math.Sqrt(-2*math.Log(u1)) * math.Cos(2*math.Pi*u2)
}

func lognormal(median, sigma, u1, u2 float64) float64 {
	return median * math.Exp(sigma*normal(u1, u2))
}

// poissonTimes draws event start minutes over the world at a weekly rate, keyed by the stream name.
func (w *World) poissonTimes(rate float64, stream ...any) []int {
	total := w.Cfg.Days * 24 * 60
	perMin := rate / (7 * 24 * 60)
	var out []int
	t := 0.0
	for i := 0; ; i++ {
		u := w.u(append(append([]any{}, stream...), "gap", i)...)
		t += -math.Log(1-u) / perMin
		if int(t) >= total {
			return out
		}
		out = append(out, int(t))
	}
}

func (w *World) drawOutages() {
	kinds := make([]string, 0, len(w.Cfg.Outages.Kinds))
	for k := range w.Cfg.Outages.Kinds {
		kinds = append(kinds, k)
	}
	sort.Strings(kinds)
	total := 0.0
	for _, k := range kinds {
		total += w.Cfg.Outages.Kinds[k].Weight
	}
	n := 0
	for _, s := range w.Cfg.Services {
		for i, start := range w.poissonTimes(s.RatePerWeek, "outage", s.ID) {
			x := w.u("kind", s.ID, i) * total
			kind := kinds[len(kinds)-1]
			for _, k := range kinds {
				x -= w.Cfg.Outages.Kinds[k].Weight
				if x < 0 {
					kind = k
					break
				}
			}
			spec := w.Cfg.Outages.Kinds[kind]
			d := lognormal(spec.MedianMin, spec.Sigma, w.u("dur1", s.ID, i), w.u("dur2", s.ID, i))
			d = math.Max(w.Cfg.Outages.MinMin, math.Min(w.Cfg.Outages.MaxMin, d))
			o := Outage{Service: s.ID, Kind: kind, Start: w.Cfg.Start.Add(time.Duration(start) * time.Minute), fail: spec.Fail}
			o.End = o.Start.Add(time.Duration(math.Round(d)) * time.Minute)
			ids := []string{}
			for _, ch := range s.Checks {
				ids = append(ids, ch.ID)
			}
			sort.Strings(ids)
			switch kind {
			case "partial":
				o.Check = ids[int(w.u("which", s.ID, i)*float64(len(ids)))]
				o.Affected = []string{o.Check}
			case "regional":
				o.Location = w.Cfg.Locations[int(w.u("where", s.ID, i)*float64(len(w.Cfg.Locations)))]
				o.Affected = ids
			case "full":
				o.Affected = ids
				for _, dep := range w.cascade[s.ID] {
					for _, ch := range w.svcOf[dep].Checks {
						o.Affected = append(o.Affected, ch.ID)
					}
				}
			default:
				o.Affected = ids
			}
			sort.Strings(o.Affected)
			pub := map[string]bool{}
			for _, cid := range o.Affected {
				comp := w.checkOf[cid].Component
				for _, c := range w.Cfg.Components {
					if c.ID == comp && c.Public {
						pub[comp] = true
					}
				}
			}
			for c := range pub {
				o.Public = append(o.Public, c)
			}
			sort.Strings(o.Public)
			w.Outages = append(w.Outages, o)
			n++
		}
	}
	sort.Slice(w.Outages, func(i, j int) bool {
		if !w.Outages[i].Start.Equal(w.Outages[j].Start) {
			return w.Outages[i].Start.Before(w.Outages[j].Start)
		}
		return w.Outages[i].Service < w.Outages[j].Service
	})
	for i := range w.Outages {
		w.Outages[i].ID = fmt.Sprintf("out-%03d", i+1)
	}
}

func (w *World) drawBlips() {
	for _, l := range w.Cfg.Locations {
		for i, start := range w.poissonTimes(w.Cfg.Blips.PerLocationPerWeek, "blip", l) {
			span := w.Cfg.Blips.MaxMin - w.Cfg.Blips.MinMin + 1
			d := w.Cfg.Blips.MinMin + int(w.u("blipdur", l, i)*float64(span))
			b := Blip{Location: l, Start: w.Cfg.Start.Add(time.Duration(start) * time.Minute)}
			b.End = b.Start.Add(time.Duration(d) * time.Minute)
			w.Blips = append(w.Blips, b)
		}
	}
	sort.Slice(w.Blips, func(i, j int) bool { return w.Blips[i].Start.Before(w.Blips[j].Start) })
}

// failChance is the chance one run fails, from every effect active at that minute.
func (w *World) failChance(ch oncall.Check, loc string, at time.Time) (float64, string) {
	p, why := w.flaky[ch.ID], "flake"
	for i := range w.Outages {
		o := &w.Outages[i]
		if at.Before(o.Start) || !at.Before(o.End) {
			continue
		}
		q := 0.0
		switch {
		case o.Kind == "regional":
			if o.Service == ch.Service && loc == o.Location {
				q = o.fail
			}
		case o.Kind == "partial":
			if o.Check == ch.ID {
				q = o.fail
			}
		case o.Service == ch.Service:
			q = o.fail
		case o.Kind == "full" && contains(w.cascade[o.Service], ch.Service):
			q = w.Cfg.Outages.CascadeFail
		}
		if q > p {
			p, why = q, o.ID
		}
	}
	for _, b := range w.Blips {
		if b.Location == loc && !at.Before(b.Start) && at.Before(b.End) && w.Cfg.Blips.Fail > p {
			p, why = w.Cfg.Blips.Fail, "blip"
		}
	}
	return p, why
}

// Results returns the runs of minute m (counted from Start), in delivery order, duplicates included.
func (w *World) Results(m int) []oncall.Result {
	at := w.Cfg.Start.Add(time.Duration(m) * time.Minute)
	out := make([]oncall.Result, 0, len(w.checks)*len(w.Cfg.Locations))
	for _, ch := range w.checks {
		for li, loc := range ch.Locations {
			p, why := w.failChance(ch, loc, at)
			x := mix(w.base(ch.ID, loc), uint64(m))
			r := oncall.Result{
				ID: "r-" + ch.ID + "|" + loc + "|" + strconv.Itoa(m), CheckID: ch.ID, Location: loc,
				At: at.Add(time.Duration(5+li*13) * time.Second), OK: unit(x) >= p,
				LatencyMs: 120 + int(unit(x^0xA5A5)*180),
			}
			if !r.OK {
				r.LatencyMs = 30000
				r.Error = "timeout"
				if why == "flake" {
					r.Error = "assertion failed"
				}
			}
			out = append(out, r)
			if unit(x^0x5A5A) < w.Cfg.Delivery.Duplicate {
				out = append(out, r)
			}
		}
	}
	return out
}

// Minutes is the number of minutes the world runs.
func (w *World) Minutes() int { return w.Cfg.Days * 24 * 60 }

// Respond is how a person answers a page: when they acknowledge it, or false if they never do. Fatigue grows
// with every page they received in the 24 hours before, whatever it was about.
func (w *World) Respond(p oncall.Page) (time.Time, bool) {
	pp := w.Cfg.People
	night := oncall.Night(p.At)
	recent := 0
	kept := w.received[p.Responder][:0]
	for _, t := range w.received[p.Responder] {
		if p.At.Sub(t) <= 24*time.Hour {
			kept = append(kept, t)
			recent++
		}
	}
	w.received[p.Responder] = append(kept, p.At)
	miss := pp.MissDay
	median := pp.AckDayMin
	if night {
		miss, median = pp.MissNight, pp.AckNightMin
	}
	miss += math.Min(pp.FatigueCap, pp.FatiguePerPage*float64(recent))
	minute := p.At.Unix() / 60
	if w.u("miss", p.Responder, minute) < miss {
		return time.Time{}, false
	}
	d := lognormal(median, pp.AckSigma, w.u("ack1", p.Responder, minute), w.u("ack2", p.Responder, minute))
	return p.At.Add(time.Duration(math.Max(0.5, d) * float64(time.Minute))).Truncate(time.Second), true
}

// HumanPost is whether, and when, a responder who acknowledged at ack writes a status post by hand.
func (w *World) HumanPost(who string, ack time.Time) (time.Time, bool) {
	pp := w.Cfg.People
	minute := ack.Unix() / 60
	if w.u("post", who, minute) >= pp.PostChance {
		return time.Time{}, false
	}
	d := lognormal(pp.PostMin, pp.PostSigma, w.u("post1", who, minute), w.u("post2", who, minute))
	return ack.Add(time.Duration(math.Max(1, d) * float64(time.Minute))).Truncate(time.Second), true
}

// HumanText is what a responder writes: plain and correct about the components they were told about.
func (w *World) HumanText(names []string) string {
	return fmt.Sprintf("We're looking into problems with %s. Updates to follow.", joinNames(names))
}

func joinNames(xs []string) string {
	switch len(xs) {
	case 0:
		return "the service"
	case 1:
		return xs[0]
	}
	s := ""
	for i, x := range xs {
		switch {
		case i == 0:
			s = x
		case i == len(xs)-1:
			s += " and " + x
		default:
			s += ", " + x
		}
	}
	return s
}

func contains(xs []string, x string) bool {
	for _, y := range xs {
		if y == x {
			return true
		}
	}
	return false
}

// ActiveAt returns the outages active at a moment, with a margin on each side.
func (w *World) ActiveAt(at time.Time, before, after time.Duration) []Outage {
	var out []Outage
	for _, o := range w.Outages {
		if !at.Before(o.Start.Add(-before)) && !at.After(o.End.Add(after)) {
			out = append(out, o)
		}
	}
	return out
}
