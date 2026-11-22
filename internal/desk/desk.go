// Package desk turns check results into incidents, pages and status posts.
//
// Every minute it decides, per check, whether the check is down: enough locations failing together, or one
// location failing long enough to call a regional problem. A location whose checks nearly all fail while the
// other locations stay green is suspect (the monitoring location's network, not the customer's service) and is
// left out for a while. Down checks group into one incident per service; a failing dependency becomes the root
// and its dependants' incidents hang under it, so one cause pages one person. An unacknowledged root escalates
// through the policy. Public components get a status post drafted by a model and checked by code.
package desk

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"sync"
	"time"

	"github.com/manojrevenkar12-web/oncall-incident-desk/internal/drafts"
	"github.com/manojrevenkar12-web/oncall-incident-desk/internal/oncall"
	"github.com/manojrevenkar12-web/oncall-incident-desk/internal/store"
)

// Level is one step of the escalation policy: page Target ("primary", "secondary", "manager") once the root
// incident has gone unacknowledged for AfterMin minutes.
type Level struct {
	AfterMin int    `json:"after_min"`
	Target   string `json:"target"`
}

// Settings are the desk's rules. A location is failing once its last ConsecutiveRuns runs failed. A check is
// down when ConfirmLocations locations are failing with at least ConfirmFailures failed runs between them, or
// when one location has failed RegionalRuns runs in a row (a regional, minor incident). They live in config/desk.json; a design in the lab is a set of settings.
type Settings struct {
	ConfirmLocations int `json:"confirm_locations"`
	ConfirmFailures  int `json:"confirm_failures"`
	ConsecutiveRuns  int `json:"consecutive_runs"`
	// A check whose runs failed more than NoisyShare of the time over the last day, outside incidents, needs
	// NoisyConfirmFailures failed runs instead: a flaky check has to fail once more before it pages.
	NoisyShare           float64 `json:"noisy_share"`
	NoisyConfirmFailures int     `json:"noisy_confirm_failures"`
	RegionalRuns         int     `json:"regional_runs"`
	RecoverRuns          int     `json:"recover_runs"`
	BlipFilter           bool    `json:"blip_filter"`
	BlipShare            float64 `json:"blip_share"`
	BlipOthersMax        float64 `json:"blip_others_max"`
	BlipMinChecks        int     `json:"blip_min_checks"`
	BlipHoldMin          int     `json:"blip_hold_min"`
	GroupByService       bool    `json:"group_by_service"`
	RollUp               bool    `json:"roll_up"`
	RollUpWindowMin      int     `json:"roll_up_window_min"`
	ReopenGraceMin       int     `json:"reopen_grace_min"`
	StaleMin             int     `json:"stale_min"`
	Escalation           []Level `json:"escalation"`
	AutoPublish          bool    `json:"auto_publish"`
	PublishMinor         bool    `json:"publish_minor"`
	CheckDrafts          bool    `json:"check_drafts"`
}

// Validate reports settings the desk cannot run with.
func (s Settings) Validate() error {
	switch {
	case s.ConfirmLocations < 1:
		return errors.New("confirm_locations must be at least 1")
	case s.ConsecutiveRuns < 1:
		return errors.New("consecutive_runs must be at least 1")
	case s.RecoverRuns < 1:
		return errors.New("recover_runs must be at least 1")
	case len(s.Escalation) == 0 || s.Escalation[0].AfterMin != 0:
		return errors.New("the escalation policy must start with a level at after_min 0")
	}
	for i := 1; i < len(s.Escalation); i++ {
		if s.Escalation[i].AfterMin <= s.Escalation[i-1].AfterMin {
			return errors.New("escalation levels must come at increasing after_min")
		}
	}
	return nil
}

// Notifier delivers pages. The server sends them out; the lab hands them to the simulated responders.
type Notifier interface {
	Notify(p oncall.Page)
}

// CheckView is what a judge sees of one check: consecutive failures and passes per location.
type CheckView struct {
	Check  oncall.Check
	At     time.Time
	Fails  map[string]int
	Passes map[string]int
	Down   bool // the judge's own last verdict
}

// Judge, when set, replaces the rules deciding whether a check is down. The lab uses it for a model that
// reads the raw results and decides on its own.
type Judge interface {
	Down(v CheckView) bool
}

type locState struct {
	fails, passes int
	last          time.Time
}

type checkState struct {
	// hourly runs and failures outside incidents over the last 24 hours, for the noise floor
	runs, fails [24]int
	hour        [24]int64
	down        bool
	regional    bool
	failing     []string
}

type bucket struct{ total, failed int }

type live struct {
	inc    oncall.Incident
	active map[string]bool
	// posted is the public components named in the last investigating post.
	posted map[string]bool
}

// Desk is safe for concurrent use.
type Desk struct {
	mu       sync.Mutex
	ix       *oncall.Index
	st       store.Store
	set      Settings
	drafter  drafts.Drafter
	checker  *drafts.Checker
	judge    Judge
	notifier Notifier

	loc      map[string]map[string]*locState
	checks   map[string]*checkState
	buckets  map[string]map[int64]*bucket
	suspect  map[string]time.Time
	incs     map[string]*live
	byKey    map[string]string
	lastEval int64
	seq      int
	// Rejected counts drafts the checker refused, by violation kind.
	Rejected map[string]int
	// Drafted counts drafts written; Fallbacks how many were replaced by the template.
	Drafted, Fallbacks int
}

// Option configures a desk.
type Option func(*Desk)

// WithDrafter sets who writes the words of a post (the template by default).
func WithDrafter(d drafts.Drafter) Option { return func(k *Desk) { k.drafter = d } }

// WithJudge hands the down/up decision to a judge.
func WithJudge(j Judge) Option { return func(k *Desk) { k.judge = j } }

// WithNotifier sets where pages go.
func WithNotifier(n Notifier) Option { return func(k *Desk) { k.notifier = n } }

// New builds a desk over a catalogue and a store.
func New(ix *oncall.Index, st store.Store, set Settings, opts ...Option) (*Desk, error) {
	if err := set.Validate(); err != nil {
		return nil, err
	}
	d := &Desk{
		ix: ix, st: st, set: set,
		drafter:  drafts.Template{},
		checker:  drafts.NewChecker(ix),
		loc:      map[string]map[string]*locState{},
		checks:   map[string]*checkState{},
		buckets:  map[string]map[int64]*bucket{},
		suspect:  map[string]time.Time{},
		incs:     map[string]*live{},
		byKey:    map[string]string{},
		lastEval: -1,
		Rejected: map[string]int{},
	}
	for _, o := range opts {
		o(d)
	}
	for _, c := range ix.Checks {
		d.loc[c.ID] = map[string]*locState{}
		for _, l := range c.Locations {
			d.loc[c.ID][l] = &locState{}
		}
		d.checks[c.ID] = &checkState{}
	}
	return d, nil
}

// ErrUnknown is returned for a check, location or incident the desk does not know.
var ErrUnknown = errors.New("unknown")

// Ingest takes one result. It returns false for a result already seen. A result older than the newest one for
// its check and location is kept but does not change the state: a late delivery cannot rewind a check.
func (d *Desk) Ingest(ctx context.Context, r oncall.Result) (bool, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	locs, ok := d.loc[r.CheckID]
	if !ok {
		return false, fmt.Errorf("%w check %q: add it to the catalogue first", ErrUnknown, r.CheckID)
	}
	ls, ok := locs[r.Location]
	if !ok {
		return false, fmt.Errorf("%w location %q for check %q", ErrUnknown, r.Location, r.CheckID)
	}
	if r.ID == "" {
		return false, errors.New("a result needs an id")
	}
	fresh, err := d.st.SaveResult(ctx, r)
	if err != nil || !fresh {
		return false, err
	}
	d.apply(r, ls)
	return true, nil
}

func (d *Desk) apply(r oncall.Result, ls *locState) {
	if !r.At.After(ls.last) && !ls.last.IsZero() {
		return
	}
	ls.last = r.At
	if r.OK {
		ls.passes++
		ls.fails = 0
	} else {
		ls.fails++
		ls.passes = 0
	}
	if cs := d.checks[r.CheckID]; !cs.down {
		h := r.At.Unix() / 3600
		i := h % 24
		if cs.hour[i] != h {
			cs.hour[i], cs.runs[i], cs.fails[i] = h, 0, 0
		}
		cs.runs[i]++
		if !r.OK {
			cs.fails[i]++
		}
	}
	m := r.At.Unix() / 60
	bs := d.buckets[r.Location]
	if bs == nil {
		bs = map[int64]*bucket{}
		d.buckets[r.Location] = bs
	}
	b := bs[m]
	if b == nil {
		b = &bucket{}
		bs[m] = b
	}
	b.total++
	if !r.OK {
		b.failed++
	}
}

// Tick evaluates the last complete minute before now: suspect locations, down and recovered checks,
// incidents, escalation and posts. Calling it twice for the same minute does nothing.
func (d *Desk) Tick(ctx context.Context, now time.Time) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	m := now.Unix()/60 - 1
	if m <= d.lastEval {
		return nil
	}
	d.lastEval = m
	now = time.Unix((m+1)*60, 0).UTC()
	if d.set.BlipFilter {
		d.findSuspects(m, now)
	}
	for loc, bs := range d.buckets {
		for k := range bs {
			if k < m-10 {
				delete(bs, k)
			}
		}
		_ = loc
	}
	var wentDown, recovered []string
	for _, svc := range d.ix.Order {
		for _, cid := range d.ix.ChecksOf[svc] {
			was := d.checks[cid].down
			d.evaluate(cid, now)
			cs := d.checks[cid]
			switch {
			case cs.down && !was:
				wentDown = append(wentDown, cid)
			case !cs.down && was:
				recovered = append(recovered, cid)
			}
		}
	}
	for _, cid := range recovered {
		if err := d.recover(ctx, cid, now); err != nil {
			return err
		}
	}
	for _, cid := range wentDown {
		if err := d.open(ctx, cid, now); err != nil {
			return err
		}
	}
	for _, cid := range d.downChecks() {
		d.refresh(cid)
	}
	if err := d.promote(ctx, now); err != nil {
		return err
	}
	if err := d.escalate(ctx, now); err != nil {
		return err
	}
	if d.set.AutoPublish {
		if err := d.publish(ctx, now); err != nil {
			return err
		}
	}
	d.forget(now)
	return nil
}

func (d *Desk) findSuspects(m int64, now time.Time) {
	type share struct{ failed, total int }
	per := map[string]share{}
	all := share{}
	for loc, bs := range d.buckets {
		if b := bs[m]; b != nil {
			per[loc] = share{b.failed, b.total}
			all.failed += b.failed
			all.total += b.total
		}
	}
	for loc, s := range per {
		if s.total < d.set.BlipMinChecks {
			continue
		}
		others := share{all.failed - s.failed, all.total - s.total}
		if others.total == 0 {
			continue
		}
		mine := float64(s.failed) / float64(s.total)
		rest := float64(others.failed) / float64(others.total)
		if mine >= d.set.BlipShare && rest <= d.set.BlipOthersMax {
			d.suspect[loc] = now.Add(time.Duration(d.set.BlipHoldMin) * time.Minute)
		}
	}
}

func (d *Desk) isSuspect(loc string, now time.Time) bool {
	until, ok := d.suspect[loc]
	return ok && now.Before(until)
}

func (d *Desk) evaluate(cid string, now time.Time) {
	cs := d.checks[cid]
	ch := d.ix.Check[cid]
	stale := time.Duration(d.set.StaleMin) * time.Minute
	var failing []string
	maxRun, total := 0, 0
	usable := 0
	allPass := true
	for _, l := range ch.Locations {
		ls := d.loc[cid][l]
		if ls.last.IsZero() || (stale > 0 && now.Sub(ls.last) > stale) || d.isSuspect(l, now) {
			continue
		}
		usable++
		if ls.fails >= d.set.ConsecutiveRuns {
			failing = append(failing, l)
			total += ls.fails
			if ls.fails > maxRun {
				maxRun = ls.fails
			}
		}
		if ls.passes < d.set.RecoverRuns {
			allPass = false
		}
	}
	if d.judge != nil {
		v := CheckView{Check: ch, At: now, Fails: map[string]int{}, Passes: map[string]int{}, Down: cs.down}
		for _, l := range ch.Locations {
			v.Fails[l] = d.loc[cid][l].fails
			v.Passes[l] = d.loc[cid][l].passes
		}
		cs.down = d.judge.Down(v)
		cs.failing = failing
		cs.regional = len(failing) == 1
		return
	}
	if !cs.down {
		need := d.set.ConfirmFailures
		if d.set.NoisyConfirmFailures > need && d.noisy(cs, now) {
			need = d.set.NoisyConfirmFailures
		}
		confirmed := len(failing) >= d.set.ConfirmLocations && total >= need
		regional := d.set.RegionalRuns > 0 && maxRun >= d.set.RegionalRuns
		if confirmed || regional {
			cs.down = true
			cs.regional = !confirmed
			cs.failing = failing
		}
		return
	}
	cs.failing = failing
	if len(failing) >= 2 || (d.set.ConfirmLocations == 1 && len(failing) >= 1) {
		cs.regional = false
	}
	if usable > 0 && allPass {
		cs.down = false
		cs.failing = nil
	}
}

// noisy reports whether a check failed more often than the noise floor over the last 24 hours.
func (d *Desk) noisy(cs *checkState, now time.Time) bool {
	if d.set.NoisyShare <= 0 {
		return false
	}
	h := now.Unix() / 3600
	runs, fails := 0, 0
	for i := range cs.hour {
		if h-cs.hour[i] < 24 {
			runs += cs.runs[i]
			fails += cs.fails[i]
		}
	}
	return runs >= 200 && float64(fails) > d.set.NoisyShare*float64(runs)
}

// Noisy lists the checks above the noise floor now, for the console.
func (d *Desk) Noisy(now time.Time) []string {
	d.mu.Lock()
	defer d.mu.Unlock()
	var out []string
	for _, c := range d.ix.Checks {
		if d.noisy(d.checks[c.ID], now) {
			out = append(out, c.ID)
		}
	}
	return out
}

func (d *Desk) key(cid string) string {
	if d.set.GroupByService {
		return d.ix.Check[cid].Service
	}
	return cid
}

func (d *Desk) nextID(prefix string) string {
	d.seq++
	return fmt.Sprintf("%s-%06d", prefix, d.seq)
}

func (d *Desk) open(ctx context.Context, cid string, now time.Time) error {
	ch := d.ix.Check[cid]
	key := d.key(cid)
	if id, ok := d.byKey[key]; ok {
		lv := d.incs[id]
		if lv.inc.Open() {
			lv.active[cid] = true
			lv.inc.Checks = addUnique(lv.inc.Checks, cid)
			return d.save(ctx, lv)
		}
		grace := time.Duration(d.set.ReopenGraceMin) * time.Minute
		if grace > 0 && now.Sub(*lv.inc.ResolvedAt) <= grace {
			lv.inc.ResolvedAt = nil
			lv.active[cid] = true
			lv.inc.Checks = addUnique(lv.inc.Checks, cid)
			return d.save(ctx, lv)
		}
	}
	lv := &live{
		inc: oncall.Incident{
			ID: d.nextID("inc"), Key: key, Service: ch.Service, Checks: []string{cid}, OpenedAt: now,
			Severity: "major",
		},
		active: map[string]bool{cid: true},
		posted: map[string]bool{},
	}
	d.incs[lv.inc.ID] = lv
	d.byKey[key] = lv.inc.ID
	d.refresh(cid)
	if d.set.RollUp {
		if root := d.failingDependency(ch.Service); root != nil {
			lv.inc.RootID = root.inc.ID
			return d.save(ctx, lv)
		}
	}
	if err := d.save(ctx, lv); err != nil {
		return err
	}
	if d.set.RollUp {
		if err := d.adoptDependants(ctx, lv, now); err != nil {
			return err
		}
	}
	return d.page(ctx, lv, 0, now)
}

// failingDependency returns the open root incident of a service this one depends on, directly or not.
func (d *Desk) failingDependency(svc string) *live {
	seen := map[string]bool{}
	var walk func(string) *live
	walk = func(s string) *live {
		deps := append([]string(nil), d.ix.Service[s].DependsOn...)
		sort.Strings(deps)
		for _, dep := range deps {
			if seen[dep] {
				continue
			}
			seen[dep] = true
			for _, lv := range d.sortedLive() {
				if lv.inc.Open() && lv.inc.Service == dep && lv.inc.RootID == "" {
					return lv
				}
			}
			if lv := walk(dep); lv != nil {
				return lv
			}
		}
		return nil
	}
	return walk(svc)
}

// adoptDependants hangs recently opened incidents of services that depend on root's service under it.
func (d *Desk) adoptDependants(ctx context.Context, root *live, now time.Time) error {
	window := time.Duration(d.set.RollUpWindowMin) * time.Minute
	below := map[string]bool{}
	var walk func(string)
	walk = func(s string) {
		for _, x := range d.ix.Downstream[s] {
			if !below[x] {
				below[x] = true
				walk(x)
			}
		}
	}
	walk(root.inc.Service)
	for _, lv := range d.sortedLive() {
		if lv == root || !lv.inc.Open() || lv.inc.RootID != "" || !below[lv.inc.Service] {
			continue
		}
		if now.Sub(lv.inc.OpenedAt) > window || lv.inc.AckedAt != nil {
			continue
		}
		lv.inc.RootID = root.inc.ID
		if err := d.save(ctx, lv); err != nil {
			return err
		}
		for _, x := range d.sortedLive() {
			if x.inc.RootID == lv.inc.ID {
				x.inc.RootID = root.inc.ID
				if err := d.save(ctx, x); err != nil {
					return err
				}
			}
		}
	}
	return nil
}

func (d *Desk) recover(ctx context.Context, cid string, now time.Time) error {
	id, ok := d.byKey[d.key(cid)]
	if !ok {
		return nil
	}
	lv := d.incs[id]
	if !lv.inc.Open() || !lv.active[cid] {
		return nil
	}
	delete(lv.active, cid)
	if len(lv.active) > 0 {
		return d.save(ctx, lv)
	}
	t := now
	lv.inc.ResolvedAt = &t
	if err := d.save(ctx, lv); err != nil {
		return err
	}
	if d.set.AutoPublish && len(lv.posted) > 0 {
		if err := d.post(ctx, lv, "resolved", keys(lv.posted), now); err != nil {
			return err
		}
	}
	return nil
}

// promote turns children whose root resolved at least two minutes ago, and that still fail, into roots of their
// own and pages their owners: they have a cause of their own. The wait lets a dependant recover a minute after
// its dependency without waking anybody.
func (d *Desk) promote(ctx context.Context, now time.Time) error {
	for _, x := range d.sortedLive() {
		if !x.inc.Open() || x.inc.RootID == "" {
			continue
		}
		root, ok := d.incs[x.inc.RootID]
		if ok && (root.inc.Open() || now.Sub(*root.inc.ResolvedAt) < 2*time.Minute) {
			continue
		}
		x.inc.RootID = ""
		x.inc.OpenedAt = now
		if err := d.save(ctx, x); err != nil {
			return err
		}
		if err := d.page(ctx, x, 0, now); err != nil {
			return err
		}
	}
	return nil
}

// refresh recomputes an incident's locations and severity from its active checks.
func (d *Desk) refresh(cid string) {
	id, ok := d.byKey[d.key(cid)]
	if !ok {
		return
	}
	lv := d.incs[id]
	locs := map[string]bool{}
	minor := true
	for c := range lv.active {
		cs := d.checks[c]
		for _, l := range cs.failing {
			locs[l] = true
		}
		if !cs.regional {
			minor = false
		}
	}
	for l := range locs {
		lv.inc.Locations = addUnique(lv.inc.Locations, l)
	}
	if minor && len(lv.active) > 0 {
		lv.inc.Severity = "minor"
	} else {
		lv.inc.Severity = "major"
	}
}

func (d *Desk) downChecks() []string {
	var out []string
	for _, c := range d.ix.Checks {
		if d.checks[c.ID].down {
			out = append(out, c.ID)
		}
	}
	return out
}

func (d *Desk) page(ctx context.Context, lv *live, level int, now time.Time) error {
	svc := d.ix.Service[lv.inc.Service]
	target := d.set.Escalation[level].Target
	who := d.ix.OnCall(svc.Team, target, now)
	lv.inc.Level = level
	if err := d.save(ctx, lv); err != nil {
		return err
	}
	if who == "" {
		return nil
	}
	p := oncall.Page{ID: d.nextID("pg"), IncidentID: lv.inc.ID, Responder: who, Level: target, At: now}
	if err := d.st.AddPage(ctx, p); err != nil {
		return err
	}
	if d.notifier != nil {
		d.notifier.Notify(p)
	}
	return nil
}

func (d *Desk) escalate(ctx context.Context, now time.Time) error {
	for _, lv := range d.sortedLive() {
		if !lv.inc.Open() || lv.inc.RootID != "" || lv.inc.AckedAt != nil {
			continue
		}
		next := lv.inc.Level + 1
		if next >= len(d.set.Escalation) {
			continue
		}
		if now.Sub(lv.inc.OpenedAt) >= time.Duration(d.set.Escalation[next].AfterMin)*time.Minute {
			if err := d.page(ctx, lv, next, now); err != nil {
				return err
			}
		}
	}
	return nil
}

// publicComponents returns the public components behind a root incident and its children's active checks.
func (d *Desk) publicComponents(root *live) []string {
	set := map[string]bool{}
	add := func(lv *live) {
		for c := range lv.active {
			comp := d.ix.Component[d.ix.Check[c].Component]
			if comp.Public {
				set[comp.ID] = true
			}
		}
	}
	add(root)
	for _, lv := range d.sortedLive() {
		if lv.inc.RootID == root.inc.ID && lv.inc.Open() {
			add(lv)
		}
	}
	return keys(set)
}

func (d *Desk) publish(ctx context.Context, now time.Time) error {
	for _, lv := range d.sortedLive() {
		if !lv.inc.Open() || lv.inc.RootID != "" {
			continue
		}
		if lv.inc.Severity == "minor" && !d.set.PublishMinor {
			continue
		}
		comps := d.publicComponents(lv)
		grew := false
		for _, c := range comps {
			if !lv.posted[c] {
				grew = true
			}
		}
		if !grew {
			continue
		}
		all := map[string]bool{}
		for c := range lv.posted {
			all[c] = true
		}
		for _, c := range comps {
			all[c] = true
		}
		if err := d.post(ctx, lv, "investigating", keys(all), now); err != nil {
			return err
		}
	}
	return nil
}

func (d *Desk) facts(lv *live, kind string, comps []string) drafts.Facts {
	f := drafts.Facts{IncidentID: lv.inc.ID, Kind: kind, Started: lv.inc.OpenedAt}
	for _, c := range comps {
		f.Components = append(f.Components, d.ix.Component[c])
	}
	if lv.inc.Severity == "minor" {
		f.Regions = append([]string(nil), lv.inc.Locations...)
	}
	return f
}

func (d *Desk) post(ctx context.Context, lv *live, kind string, comps []string, now time.Time) error {
	f := d.facts(lv, kind, comps)
	text, violations, fallback := drafts.Decide(ctx, d.drafter, d.checker, f, d.set.CheckDrafts)
	d.Drafted++
	if fallback {
		d.Fallbacks++
	}
	for _, v := range violations {
		d.Rejected[v.Kind]++
	}
	author := "desk"
	if _, isTemplate := d.drafter.(drafts.Template); !isTemplate && !fallback {
		author = "model"
	}
	p := oncall.Post{ID: d.nextID("po"), IncidentID: lv.inc.ID, At: now, Kind: kind, Components: comps, Text: text,
		Author: author, Checked: d.set.CheckDrafts, Fallback: fallback}
	for _, c := range comps {
		lv.posted[c] = true
	}
	return d.st.AddPost(ctx, p)
}

// Ack records that a person took an incident. Acknowledging a child acknowledges its root.
func (d *Desk) Ack(ctx context.Context, incidentID, who string, at time.Time) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	lv, ok := d.incs[incidentID]
	if !ok {
		inc, found, err := d.st.Incident(ctx, incidentID)
		if err != nil {
			return err
		}
		if !found {
			return fmt.Errorf("%w incident %q", ErrUnknown, incidentID)
		}
		if inc.AckedAt == nil {
			t := at.UTC()
			inc.AckedAt, inc.AckedBy = &t, who
			return d.st.PutIncident(ctx, inc)
		}
		return nil
	}
	for {
		if lv.inc.AckedAt == nil {
			t := at.UTC()
			lv.inc.AckedAt, lv.inc.AckedBy = &t, who
			if err := d.save(ctx, lv); err != nil {
				return err
			}
		}
		if lv.inc.RootID == "" {
			return nil
		}
		root, ok := d.incs[lv.inc.RootID]
		if !ok {
			return nil
		}
		lv = root
	}
}

// ErrRejected is returned when a person's post fails the checker; the violations say what to change.
type ErrRejected struct{ Violations []drafts.Violation }

func (e ErrRejected) Error() string {
	s := "the post was not published:"
	for _, v := range e.Violations {
		s += fmt.Sprintf(" %s (%s);", v.Kind, v.Detail)
	}
	return s
}

// PostManual publishes a person's words for an incident's public components. When CheckDrafts is on the checker
// runs on them too, and a failing post is refused with the reasons rather than replaced.
func (d *Desk) PostManual(ctx context.Context, incidentID, who, text string, at time.Time) (oncall.Post, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	lv, ok := d.incs[incidentID]
	if !ok {
		return oncall.Post{}, fmt.Errorf("%w incident %q", ErrUnknown, incidentID)
	}
	for lv.inc.RootID != "" {
		if r, ok := d.incs[lv.inc.RootID]; ok {
			lv = r
		} else {
			break
		}
	}
	kind := "investigating"
	comps := d.publicComponents(lv)
	if !lv.inc.Open() {
		kind = "resolved"
		comps = keys(lv.posted)
		if len(comps) == 0 {
			for _, c := range lv.inc.Checks {
				if comp := d.ix.Component[d.ix.Check[c].Component]; comp.Public {
					comps = addUnique(comps, comp.ID)
				}
			}
			sort.Strings(comps)
		}
	}
	if len(comps) == 0 {
		return oncall.Post{}, errors.New("this incident affects no public component, so nothing goes on the status page")
	}
	if d.set.CheckDrafts {
		if v := d.checker.Check(text, d.facts(lv, kind, comps)); len(v) > 0 {
			return oncall.Post{}, ErrRejected{v}
		}
	}
	p := oncall.Post{ID: d.nextID("po"), IncidentID: lv.inc.ID, At: at.UTC(), Kind: kind, Components: comps,
		Text: text, Author: who, Checked: d.set.CheckDrafts}
	for _, c := range comps {
		lv.posted[c] = true
	}
	return p, d.st.AddPost(ctx, p)
}

// PublicComponents returns the public components an incident's post would name now.
func (d *Desk) PublicComponents(incidentID string) []string {
	d.mu.Lock()
	defer d.mu.Unlock()
	lv, ok := d.incs[incidentID]
	if !ok {
		return nil
	}
	for lv.inc.RootID != "" {
		r, ok := d.incs[lv.inc.RootID]
		if !ok {
			break
		}
		lv = r
	}
	return d.publicComponents(lv)
}

// RootOf returns the root incident ID of an incident.
func (d *Desk) RootOf(incidentID string) string {
	d.mu.Lock()
	defer d.mu.Unlock()
	lv, ok := d.incs[incidentID]
	for ok && lv.inc.RootID != "" {
		incidentID = lv.inc.RootID
		lv, ok = d.incs[incidentID]
	}
	return incidentID
}

func (d *Desk) save(ctx context.Context, lv *live) error {
	sort.Strings(lv.inc.Checks)
	sort.Strings(lv.inc.Locations)
	return d.st.PutIncident(ctx, lv.inc)
}

// forget drops resolved incidents past the reopen grace from memory (the store keeps them).
func (d *Desk) forget(now time.Time) {
	grace := time.Duration(d.set.ReopenGraceMin) * time.Minute
	for id, lv := range d.incs {
		if lv.inc.Open() || now.Sub(*lv.inc.ResolvedAt) <= grace+time.Hour {
			continue
		}
		child := false
		for _, x := range d.incs {
			if x.inc.RootID == id && x.inc.Open() {
				child = true
			}
		}
		if child {
			continue
		}
		delete(d.incs, id)
		if d.byKey[lv.inc.Key] == id {
			delete(d.byKey, lv.inc.Key)
		}
	}
}

func (d *Desk) sortedLive() []*live {
	out := make([]*live, 0, len(d.incs))
	for _, lv := range d.incs {
		out = append(out, lv)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].inc.ID < out[j].inc.ID })
	return out
}

// Load rebuilds the desk after a restart: open incidents (and those inside the reopen grace) from the store,
// and each check's state from the results of the last window.
func (d *Desk) Load(ctx context.Context, now time.Time, window time.Duration) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	incs, err := d.st.Incidents(ctx, false)
	if err != nil {
		return err
	}
	grace := time.Duration(d.set.ReopenGraceMin) * time.Minute
	for _, inc := range incs {
		if n := seqOf(inc.ID); n > d.seq {
			d.seq = n
		}
		if !inc.Open() && now.Sub(*inc.ResolvedAt) > grace {
			continue
		}
		lv := &live{inc: inc, active: map[string]bool{}, posted: map[string]bool{}}
		if inc.Open() {
			for _, c := range inc.Checks {
				lv.active[c] = true
				if cs, ok := d.checks[c]; ok {
					cs.down = true
				}
			}
		}
		d.incs[inc.ID] = lv
		d.byKey[inc.Key] = inc.ID
	}
	pages, err := d.st.Pages(ctx)
	if err != nil {
		return err
	}
	for _, p := range pages {
		if n := seqOf(p.ID); n > d.seq {
			d.seq = n
		}
	}
	posts, err := d.st.Posts(ctx)
	if err != nil {
		return err
	}
	for _, p := range posts {
		if n := seqOf(p.ID); n > d.seq {
			d.seq = n
		}
		if lv, ok := d.incs[p.IncidentID]; ok && p.Kind == "investigating" {
			for _, c := range p.Components {
				lv.posted[c] = true
			}
		}
	}
	rs, err := d.st.ResultsSince(ctx, now.Add(-window))
	if err != nil {
		return err
	}
	for _, r := range rs {
		if ls, ok := d.loc[r.CheckID][r.Location]; ok {
			d.apply(r, ls)
		}
	}
	d.lastEval = now.Unix()/60 - 1
	return nil
}

func seqOf(id string) int {
	var prefix string
	var n int
	for i := len(id) - 1; i >= 0; i-- {
		if id[i] == '-' {
			prefix = id[i+1:]
			break
		}
	}
	fmt.Sscanf(prefix, "%d", &n)
	return n
}

func addUnique(xs []string, x string) []string {
	for _, y := range xs {
		if y == x {
			return xs
		}
	}
	return append(xs, x)
}

func keys(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
