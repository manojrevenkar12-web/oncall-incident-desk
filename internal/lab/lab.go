// Package lab plays the simulated world through each design and scores what happened against the world's
// truth. It is the only package that sees both the product and the truth.
package lab

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"time"

	"github.com/manojrevenkar12-web/oncall-incident-desk/internal/desk"
	"github.com/manojrevenkar12-web/oncall-incident-desk/internal/drafts"
	"github.com/manojrevenkar12-web/oncall-incident-desk/internal/oncall"
	"github.com/manojrevenkar12-web/oncall-incident-desk/internal/store"
	"github.com/manojrevenkar12-web/oncall-incident-desk/internal/world"
)

// Design is one way to run on-call, in config/designs.json: overrides on config/desk.json plus who writes posts.
type Design struct {
	Name       string          `json:"name"`
	Label      string          `json:"label"`
	About      string          `json:"about,omitempty"`
	Ablation   bool            `json:"ablation,omitempty"`
	Overrides  json.RawMessage `json:"settings"`
	Drafter    string          `json:"drafter"` // "model" or "template"
	Judge      bool            `json:"judge,omitempty"`
	HumanPosts bool            `json:"human_posts,omitempty"`
	Settings   desk.Settings   `json:"-"`
}

// LoadDesigns reads the base settings and the designs and applies each design's overrides.
func LoadDesigns(root string) ([]Design, desk.Settings, error) {
	var base desk.Settings
	if err := readJSON(filepath.Join(root, "config/desk.json"), &base); err != nil {
		return nil, base, err
	}
	var ds []Design
	if err := readJSON(filepath.Join(root, "config/designs.json"), &ds); err != nil {
		return nil, base, err
	}
	for i := range ds {
		b, _ := json.Marshal(base)
		var s desk.Settings
		json.Unmarshal(b, &s)
		if len(ds[i].Overrides) > 0 {
			if err := json.Unmarshal(ds[i].Overrides, &s); err != nil {
				return nil, base, fmt.Errorf("design %s: %w", ds[i].Name, err)
			}
		}
		if err := s.Validate(); err != nil {
			return nil, base, fmt.Errorf("design %s: %w", ds[i].Name, err)
		}
		ds[i].Settings = s
	}
	return ds, base, nil
}

func readJSON(path string, v any) error {
	b, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	if err := json.Unmarshal(b, v); err != nil {
		return fmt.Errorf("%s: %w", path, err)
	}
	return nil
}

// Draft is one post the model wrote, and what became of it.
type Draft struct {
	At         time.Time          `json:"at"`
	Incident   string             `json:"incident"`
	Kind       string             `json:"kind"`
	Text       string             `json:"text"`
	Injected   []string           `json:"injected"`
	Violations []drafts.Violation `json:"violations"`
	Published  bool               `json:"published"`
}

// Run is everything one design did in one seeded world.
type Run struct {
	Design    string
	Seed      uint64
	World     *world.World
	Incidents []oncall.Incident
	Pages     []oncall.Page
	Posts     []oncall.Post
	Drafts    []Draft
	Desk      *desk.Desk
}

type event struct {
	at   time.Time
	seq  int
	kind string // "ack" or "post"
	inc  string
	who  string
}

type collector struct{ pages []oncall.Page }

func (c *collector) Notify(p oncall.Page) { c.pages = append(c.pages, p) }

type modelJudge struct{ w *world.World }

func (j modelJudge) Down(v desk.CheckView) bool {
	return j.w.ModelDown(v.Check.ID, v.At, v.Fails, v.Passes, v.Down)
}

// modelDrafter hands the facts to the stand-in model and keeps a log of what it wrote.
type modelDrafter struct {
	w       *world.World
	st      store.Store
	ix      *oncall.Index
	checker *drafts.Checker
	check   bool
	log     *[]Draft
}

func (m modelDrafter) Draft(ctx context.Context, f drafts.Facts) (string, error) {
	inc, _, _ := m.st.Incident(ctx, f.IncidentID)
	affected := map[string]bool{}
	for _, c := range f.Components {
		affected[c.ID] = true
	}
	var others []string
	for _, c := range m.ix.Components {
		if c.Public && !affected[c.ID] {
			others = append(others, c.Name)
		}
	}
	var regions []string
	for _, r := range f.Regions {
		regions = append(regions, drafts.RegionName(r))
	}
	text := m.w.ModelDraft(f.IncidentID+"|"+f.Started.Format(time.RFC3339), f.Kind, f.Names(), others, regions, inc.Service)
	d := Draft{At: f.Started, Incident: f.IncidentID, Kind: f.Kind, Text: text, Injected: m.w.Injected[text]}
	if m.check {
		d.Violations = m.checker.Check(text, f)
	}
	d.Published = len(d.Violations) == 0
	*m.log = append(*m.log, d)
	return text, nil
}

// Window returns minutes around the showcase outage of a seed: an hour before it to five hours after.
func Window(cfg world.Config, phrases world.Phrases, seed uint64) (int, int) {
	w := world.New(cfg, seed, phrases)
	o, ok := pickOutage(w)
	if !ok {
		return 0, 6 * 60
	}
	m := int(o.Start.Sub(w.Cfg.Start).Minutes())
	from := m - 60
	if from < 0 {
		from = 0
	}
	return from, from + 6*60
}

// Play runs one design through one seeded world, minute by minute, on a memory store.
func Play(ctx context.Context, d Design, cfg world.Config, phrases world.Phrases, seed uint64) (*Run, error) {
	return PlayInto(ctx, d, cfg, phrases, seed, store.NewMemory(2*time.Hour), 0, 0)
}

// PlayInto runs a design on the given store over minutes [from, to) of the world (to 0 plays to the end).
func PlayInto(ctx context.Context, d Design, cfg world.Config, phrases world.Phrases, seed uint64, st store.Store, from, to int) (*Run, error) {
	w := world.New(cfg, seed, phrases)
	ix := oncall.NewIndex(w.Catalog())
	col := &collector{}
	run := &Run{Design: d.Name, Seed: seed, World: w}
	opts := []desk.Option{desk.WithNotifier(col)}
	if d.Drafter == "model" {
		opts = append(opts, desk.WithDrafter(modelDrafter{w: w, st: st, ix: ix, checker: drafts.NewChecker(ix),
			check: d.Settings.CheckDrafts, log: &run.Drafts}))
	}
	if d.Judge {
		opts = append(opts, desk.WithJudge(modelJudge{w}))
	}
	k, err := desk.New(ix, st, d.Settings, opts...)
	if err != nil {
		return nil, err
	}
	run.Desk = k
	var queue []event
	seq := 0
	push := func(e event) {
		seq++
		e.seq = seq
		queue = append(queue, e)
	}
	start := w.Cfg.Start
	limit := w.Minutes()
	if to > 0 && to < limit {
		limit = to
	}
	for m := from; m < limit; m++ {
		for _, r := range w.Results(m) {
			if _, err := k.Ingest(ctx, r); err != nil {
				return nil, err
			}
		}
		now := start.Add(time.Duration(m+1) * time.Minute)
		if err := k.Tick(ctx, now); err != nil {
			return nil, err
		}
		for _, p := range col.pages {
			if at, ok := w.Respond(p); ok {
				push(event{at: at, kind: "ack", inc: p.IncidentID, who: p.Responder})
			}
		}
		col.pages = col.pages[:0]
		sort.Slice(queue, func(i, j int) bool {
			if !queue[i].at.Equal(queue[j].at) {
				return queue[i].at.Before(queue[j].at)
			}
			return queue[i].seq < queue[j].seq
		})
		n := 0
		for n < len(queue) && !queue[n].at.After(now) {
			n++
		}
		due := append([]event(nil), queue[:n]...)
		queue = queue[n:]
		for _, e := range due {
			root := k.RootOf(e.inc)
			inc, _, _ := st.Incident(ctx, root)
			switch e.kind {
			case "ack":
				first := inc.AckedAt == nil
				if err := k.Ack(ctx, e.inc, e.who, e.at); err != nil {
					return nil, err
				}
				if first && d.HumanPosts && inc.Open() && len(k.PublicComponents(root)) > 0 {
					if at, ok := w.HumanPost(e.who, e.at); ok {
						push(event{at: at, kind: "post", inc: root, who: e.who})
					}
				}
			case "post":
				comps := k.PublicComponents(root)
				if !inc.Open() || len(comps) == 0 {
					continue
				}
				var names []string
				for _, c := range comps {
					names = append(names, ix.Component[c].Name)
				}
				if _, err := k.PostManual(ctx, root, e.who, w.HumanText(names), e.at); err != nil {
					return nil, err
				}
			}
		}
	}
	run.Incidents, _ = st.Incidents(ctx, false)
	run.Pages, _ = st.Pages(ctx)
	run.Posts, _ = st.Posts(ctx)
	return run, nil
}
