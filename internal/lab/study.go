package lab

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"hash/fnv"
	"math"
	"math/rand/v2"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"sync"
	"time"

	"github.com/manojrevenkar12-web/oncall-incident-desk/internal/oncall"
	"github.com/manojrevenkar12-web/oncall-incident-desk/internal/world"
)

// Stat is a value with its 95% bootstrap interval over seeds.
type Stat struct {
	Value float64 `json:"value"`
	Lo    float64 `json:"lo"`
	Hi    float64 `json:"hi"`
}

// DesignReport is one design's numbers over every seed.
type DesignReport struct {
	Name     string          `json:"name"`
	Label    string          `json:"label"`
	About    string          `json:"about,omitempty"`
	Ablation bool            `json:"ablation,omitempty"`
	Stats    map[string]Stat `json:"stats"`
	Totals   Totals          `json:"totals"`
}

// Totals are counts summed over seeds.
type Totals struct {
	Outages         int            `json:"outages"`
	Detected        int            `json:"detected"`
	TruePages       int            `json:"true_pages"`
	FalsePages      int            `json:"false_pages"`
	NightFalsePages int            `json:"night_false_pages"`
	Posts           int            `json:"posts"`
	WrongPosts      int            `json:"wrong_posts"`
	BadClaims       int            `json:"bad_claims"`
	Drafts          int            `json:"drafts"`
	DraftsWithError int            `json:"drafts_with_error"`
	Caught          int            `json:"caught"`
	CleanRejected   int            `json:"clean_rejected"`
	MissedKinds     map[string]int `json:"missed_kinds"`
}

// Gate is one release condition, checked on the desk design.
type Gate struct {
	Name string  `json:"name"`
	Rule string  `json:"rule"`
	Want float64 `json:"want"`
	Got  float64 `json:"got"`
	Pass bool    `json:"pass"`
}

// Example is a draft the checker refused, or one that got through with an error in it.
type Example struct {
	Seed       uint64   `json:"seed"`
	Text       string   `json:"text"`
	Injected   []string `json:"injected"`
	Violations []string `json:"violations"`
}

// CheckerReport is how the claim checker did on the desk design's drafts.
type CheckerReport struct {
	Drafts          int       `json:"drafts"`
	WithError       int       `json:"with_error"`
	Caught          int       `json:"caught"`
	CleanRejected   int       `json:"clean_rejected"`
	Recall          float64   `json:"recall"`
	FalseRejectRate float64   `json:"false_reject_rate"`
	ByKind          []KindRow `json:"by_kind"`
	Refused         []Example `json:"refused"`
	Through         []Example `json:"through"`
}

// KindRow is the checker's recall for one kind of error.
type KindRow struct {
	Kind     string `json:"kind"`
	Injected int    `json:"injected"`
	Caught   int    `json:"caught"`
}

// Report is the whole study, written to reports/<world>.json.
type Report struct {
	World     string          `json:"world"`
	Seeds     []uint64        `json:"seeds"`
	Days      int             `json:"days"`
	Checks    int             `json:"checks"`
	Locations int             `json:"locations"`
	Services  int             `json:"services"`
	Designs   []DesignReport  `json:"designs"`
	Gates     []Gate          `json:"gates"`
	Checker   CheckerReport   `json:"checker"`
	Showcase  Showcase        `json:"showcase"`
	Truth     TruthSummary    `json:"truth"`
	Took      string          `json:"took"`
	Settings  json.RawMessage `json:"settings"`
}

// TruthSummary counts what the world really did.
type TruthSummary struct {
	Outages     int            `json:"outages"`
	ByKind      map[string]int `json:"by_kind"`
	Blips       int            `json:"blips"`
	NightShare  float64        `json:"night_share"`
	MedianMin   float64        `json:"median_min"`
	PublicShare float64        `json:"public_share"`
}

// Seeds is config/seeds.json.
type Seeds struct {
	Dev       []uint64 `json:"dev"`
	Heldout   []uint64 `json:"heldout"`
	Bootstrap int      `json:"bootstrap"`
}

// GateSpec is config/gates.json.
type GateSpec struct {
	RecallMin             float64 `json:"recall_min"`
	FalsePagesShareMax    float64 `json:"false_pages_share_of_status_quo_max"`
	NoticeMedianMaxMin    float64 `json:"notice_median_max_min"`
	NoticeCoverageMin     float64 `json:"notice_coverage_min"`
	BadClaimsPer100Max    float64 `json:"bad_claims_per_100_posts_max"`
	MTTAShareOfStatusQuoM float64 `json:"mtta_share_of_status_quo_max"`
}

// ErrLocked is returned when the held-out study has already run.
var ErrLocked = errors.New("reports/heldout.lock exists: the held-out study runs once. Its numbers are in reports/heldout.json")

// Study runs every design over every seed of a world ("dev" or "heldout") and writes the report.
func Study(ctx context.Context, root, which string) (*Report, error) {
	began := time.Now()
	lock := filepath.Join(root, "reports/heldout.lock")
	if which == "heldout" {
		if _, err := os.Stat(lock); err == nil {
			return nil, ErrLocked
		}
	}
	var seeds Seeds
	if err := readJSON(filepath.Join(root, "config/seeds.json"), &seeds); err != nil {
		return nil, err
	}
	var gates GateSpec
	if err := readJSON(filepath.Join(root, "config/gates.json"), &gates); err != nil {
		return nil, err
	}
	cfgPath := map[string]string{"dev": "config/world.json", "heldout": "config/heldout.json"}[which]
	if cfgPath == "" {
		return nil, fmt.Errorf("unknown world %q: use dev or heldout", which)
	}
	cfg, err := world.Load(filepath.Join(root, cfgPath))
	if err != nil {
		return nil, err
	}
	phrases, err := world.LoadPhrases(filepath.Join(root, cfg.Model.PhrasesFile))
	if err != nil {
		return nil, err
	}
	designs, base, err := LoadDesigns(root)
	if err != nil {
		return nil, err
	}
	seedList := seeds.Dev
	if which == "heldout" {
		seedList = seeds.Heldout
	}
	type job struct {
		d    int
		seed int
	}
	scores := make([][]Score, len(designs))
	for i := range scores {
		scores[i] = make([]Score, len(seedList))
	}
	runs := make([][]*Run, len(designs))
	for i := range runs {
		runs[i] = make([]*Run, len(seedList))
	}
	jobs := make(chan job)
	var wg sync.WaitGroup
	var mu sync.Mutex
	var firstErr error
	for n := 0; n < runtime.NumCPU(); n++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := range jobs {
				r, err := Play(ctx, designs[j.d], cfg, phrases, seedList[j.seed])
				mu.Lock()
				if err != nil && firstErr == nil {
					firstErr = fmt.Errorf("%s seed %d: %w", designs[j.d].Name, seedList[j.seed], err)
				}
				if err == nil {
					scores[j.d][j.seed] = Measure(r)
					r.Desk = nil
					keep := j.seed == 0 || designs[j.d].Name == "desk"
					if keep {
						runs[j.d][j.seed] = r
					}
				}
				mu.Unlock()
			}
		}()
	}
	for d := range designs {
		for s := range seedList {
			jobs <- job{d, s}
		}
	}
	close(jobs)
	wg.Wait()
	if firstErr != nil {
		return nil, firstErr
	}
	rep := &Report{World: cfg.Name, Seeds: seedList, Days: cfg.Days, Locations: len(cfg.Locations),
		Services: len(cfg.Services)}
	for _, s := range cfg.Services {
		rep.Checks += len(s.Checks)
	}
	rep.Settings, _ = json.Marshal(base)
	for i, d := range designs {
		rep.Designs = append(rep.Designs, summarize(d, scores[i], seeds.Bootstrap))
	}
	rep.Gates = checkGates(gates, rep.Designs)
	for i, d := range designs {
		if d.Name == "desk" {
			rep.Checker = checkerReport(runs[i], scores[i])
		}
	}
	var first []*Run
	for i := range designs {
		first = append(first, runs[i][0])
	}
	rep.Showcase = showcase(designs, first)
	rep.Truth = truth(cfg, phrases, seedList)
	rep.Took = time.Since(began).Round(time.Second).String()
	if err := writeReport(root, which, rep); err != nil {
		return nil, err
	}
	if which == "heldout" {
		msg := fmt.Sprintf("held-out study ran at %s; it does not run again.\n", time.Now().UTC().Format(time.RFC3339))
		if err := os.WriteFile(lock, []byte(msg), 0o644); err != nil {
			return nil, err
		}
	}
	return rep, nil
}

func writeReport(root, which string, rep *Report) error {
	b, err := json.MarshalIndent(rep, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Join(root, "reports"), 0o755); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(root, "reports", which+".json"), append(b, '\n'), 0o644)
}

// metric computes one number from a set of per-seed scores.
type metric func(ss []Score) float64

var metrics = map[string]metric{
	"recall": func(ss []Score) float64 {
		d, o := 0, 0
		for _, s := range ss {
			d += s.Detected
			o += s.Outages
		}
		return ratio(float64(d), float64(o))
	},
	"mtta_median": func(ss []Score) float64 { return quantile(pool(ss, func(s Score) []float64 { return s.MTTA }), 0.5) },
	"ttp_median":  func(ss []Score) float64 { return quantile(pool(ss, func(s Score) []float64 { return s.TTP }), 0.5) },
	"mtta_p90":    func(ss []Score) float64 { return quantile(pool(ss, func(s Score) []float64 { return s.MTTA }), 0.9) },
	"false_pages_per_week": func(ss []Score) float64 {
		return perWeek(ss, func(s Score) int { return s.FalsePages })
	},
	"night_false_pages_per_week": func(ss []Score) float64 {
		return perWeek(ss, func(s Score) int { return s.NightFalsePages })
	},
	"pages_per_outage": func(ss []Score) float64 {
		p, o := 0, 0
		for _, s := range ss {
			p += s.TruePages
			o += s.Outages
		}
		return ratio(float64(p), float64(o))
	},
	"notice_coverage": func(ss []Score) float64 {
		p, o := 0, 0
		for _, s := range ss {
			p += s.Posted
			o += s.PublicOutages
		}
		return ratio(float64(p), float64(o))
	},
	"notice_median": func(ss []Score) float64 {
		return quantile(pool(ss, func(s Score) []float64 { return s.Notice }), 0.5)
	},
	"wrong_posts_per_week": func(ss []Score) float64 { return perWeek(ss, func(s Score) int { return s.WrongPosts }) },
	"bad_claims_per_100_posts": func(ss []Score) float64 {
		b, p := 0, 0
		for _, s := range ss {
			b += s.BadClaims
			p += s.Posts
		}
		return 100 * ratio(float64(b), float64(p))
	},
}

// MetricOrder is how reports list the metrics.
var MetricOrder = []string{"recall", "ttp_median", "mtta_median", "mtta_p90", "false_pages_per_week", "night_false_pages_per_week",
	"pages_per_outage", "notice_coverage", "notice_median", "wrong_posts_per_week", "bad_claims_per_100_posts"}

func ratio(a, b float64) float64 {
	if b == 0 {
		return 0
	}
	return a / b
}

func perWeek(ss []Score, f func(Score) int) float64 {
	n, w := 0, 0.0
	for _, s := range ss {
		n += f(s)
		w += s.Weeks
	}
	return ratio(float64(n), w)
}

func pool(ss []Score, f func(Score) []float64) []float64 {
	var out []float64
	for _, s := range ss {
		out = append(out, f(s)...)
	}
	return out
}

func quantile(xs []float64, q float64) float64 {
	if len(xs) == 0 {
		return 0
	}
	ys := append([]float64(nil), xs...)
	sort.Float64s(ys)
	pos := q * float64(len(ys)-1)
	lo := int(math.Floor(pos))
	hi := int(math.Ceil(pos))
	return ys[lo] + (ys[hi]-ys[lo])*(pos-float64(lo))
}

// bootstrap resamples seeds with a fixed keyed generator, so the interval is the same on every machine.
func bootstrap(ss []Score, f metric, reps int, key string) Stat {
	st := Stat{Value: f(ss)}
	if len(ss) < 2 || reps == 0 {
		st.Lo, st.Hi = st.Value, st.Value
		return st
	}
	h := fnv.New64a()
	h.Write([]byte(key))
	rng := rand.New(rand.NewPCG(h.Sum64(), 7))
	vals := make([]float64, reps)
	sample := make([]Score, len(ss))
	for i := range vals {
		for j := range sample {
			sample[j] = ss[rng.IntN(len(ss))]
		}
		vals[i] = f(sample)
	}
	st.Lo = quantile(vals, 0.025)
	st.Hi = quantile(vals, 0.975)
	return st
}

func summarize(d Design, ss []Score, reps int) DesignReport {
	r := DesignReport{Name: d.Name, Label: d.Label, About: d.About, Ablation: d.Ablation, Stats: map[string]Stat{},
		Totals: Totals{MissedKinds: map[string]int{}}}
	for _, name := range MetricOrder {
		r.Stats[name] = round(bootstrap(ss, metrics[name], reps, d.Name+"|"+name))
	}
	for _, s := range ss {
		t := &r.Totals
		t.Outages += s.Outages
		t.Detected += s.Detected
		t.TruePages += s.TruePages
		t.FalsePages += s.FalsePages
		t.NightFalsePages += s.NightFalsePages
		t.Posts += s.Posts
		t.WrongPosts += s.WrongPosts
		t.BadClaims += s.BadClaims
		t.Drafts += s.Drafts
		t.DraftsWithError += s.DraftsWithError
		t.Caught += s.Caught
		t.CleanRejected += s.CleanRejected
		for k, v := range s.MissedKinds {
			t.MissedKinds[k] += v
		}
	}
	return r
}

func round(s Stat) Stat {
	f := func(x float64) float64 { return math.Round(x*1000) / 1000 }
	return Stat{f(s.Value), f(s.Lo), f(s.Hi)}
}

func find(ds []DesignReport, name string) DesignReport {
	for _, d := range ds {
		if d.Name == name {
			return d
		}
	}
	return DesignReport{Stats: map[string]Stat{}}
}

func checkGates(g GateSpec, ds []DesignReport) []Gate {
	desk := find(ds, "desk").Stats
	sq := find(ds, "status_quo").Stats
	share := func(a, b float64) float64 { return math.Round(ratio(a, b)*1000) / 1000 }
	return []Gate{
		{Name: "Outages acknowledged in time", Rule: "recall at least", Want: g.RecallMin, Got: desk["recall"].Value,
			Pass: desk["recall"].Value >= g.RecallMin},
		{Name: "False pages, share of the status quo", Rule: "at most", Want: g.FalsePagesShareMax,
			Got:  share(desk["false_pages_per_week"].Value, sq["false_pages_per_week"].Value),
			Pass: ratio(desk["false_pages_per_week"].Value, sq["false_pages_per_week"].Value) <= g.FalsePagesShareMax},
		{Name: "Median time to acknowledge, share of the status quo", Rule: "at most", Want: g.MTTAShareOfStatusQuoM,
			Got:  share(desk["mtta_median"].Value, sq["mtta_median"].Value),
			Pass: ratio(desk["mtta_median"].Value, sq["mtta_median"].Value) <= g.MTTAShareOfStatusQuoM},
		{Name: "Public outages announced", Rule: "coverage at least", Want: g.NoticeCoverageMin,
			Got: desk["notice_coverage"].Value, Pass: desk["notice_coverage"].Value >= g.NoticeCoverageMin},
		{Name: "Median minutes to the first post", Rule: "at most", Want: g.NoticeMedianMaxMin,
			Got: desk["notice_median"].Value, Pass: desk["notice_median"].Value <= g.NoticeMedianMaxMin},
		{Name: "Bad claims per 100 posts", Rule: "at most", Want: g.BadClaimsPer100Max,
			Got: desk["bad_claims_per_100_posts"].Value, Pass: desk["bad_claims_per_100_posts"].Value <= g.BadClaimsPer100Max},
	}
}

func checkerReport(runs []*Run, ss []Score) CheckerReport {
	c := CheckerReport{}
	injected := map[string]int{}
	caught := map[string]int{}
	for i, r := range runs {
		if r == nil {
			continue
		}
		for _, d := range r.Drafts {
			c.Drafts++
			if len(d.Injected) > 0 {
				c.WithError++
				for _, k := range d.Injected {
					injected[k]++
					if !d.Published {
						caught[k]++
					}
				}
				var vs []string
				for _, v := range d.Violations {
					vs = append(vs, v.Kind+": "+v.Detail)
				}
				ex := Example{Seed: r.Seed, Text: d.Text, Injected: d.Injected, Violations: vs}
				if d.Published {
					if len(c.Through) < 12 && !hasText(c.Through, d.Text) {
						c.Through = append(c.Through, ex)
					}
				} else {
					c.Caught++
					if len(c.Refused) < 12 && !hasText(c.Refused, d.Text) && !hasKinds(c.Refused, d.Injected) {
						c.Refused = append(c.Refused, ex)
					}
				}
			} else if !d.Published {
				c.CleanRejected++
				var vs []string
				for _, v := range d.Violations {
					vs = append(vs, v.Kind+": "+v.Detail)
				}
				if len(c.Refused) < 12 {
					c.Refused = append(c.Refused, Example{Seed: r.Seed, Text: d.Text, Violations: vs})
				}
			}
		}
		_ = i
	}
	c.Recall = math.Round(ratio(float64(c.Caught), float64(c.WithError))*1000) / 1000
	c.FalseRejectRate = math.Round(ratio(float64(c.CleanRejected), float64(c.Drafts-c.WithError))*1000) / 1000
	kinds := make([]string, 0, len(injected))
	for k := range injected {
		kinds = append(kinds, k)
	}
	sort.Strings(kinds)
	for _, k := range kinds {
		c.ByKind = append(c.ByKind, KindRow{Kind: k, Injected: injected[k], Caught: caught[k]})
	}
	_ = ss
	return c
}

func hasText(xs []Example, t string) bool {
	for _, x := range xs {
		if x.Text == t {
			return true
		}
	}
	return false
}

func hasKinds(xs []Example, ks []string) bool {
	for _, x := range xs {
		if fmt.Sprint(x.Injected) == fmt.Sprint(ks) {
			return true
		}
	}
	return false
}

func truth(cfg world.Config, ph world.Phrases, seeds []uint64) TruthSummary {
	t := TruthSummary{ByKind: map[string]int{}}
	var durs []float64
	night, public := 0, 0
	for _, s := range seeds {
		w := world.New(cfg, s, ph)
		for _, o := range w.Outages {
			t.Outages++
			t.ByKind[o.Kind]++
			durs = append(durs, o.End.Sub(o.Start).Minutes())
			if oncall.Night(o.Start) {
				night++
			}
			if len(o.Public) > 0 {
				public++
			}
		}
		t.Blips += len(w.Blips)
	}
	t.NightShare = math.Round(ratio(float64(night), float64(t.Outages))*1000) / 1000
	t.PublicShare = math.Round(ratio(float64(public), float64(t.Outages))*1000) / 1000
	t.MedianMin = quantile(durs, 0.5)
	return t
}
