package desk

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/manojrevenkar12-web/oncall-incident-desk/internal/oncall"
	"github.com/manojrevenkar12-web/oncall-incident-desk/internal/store"
)

var (
	ctx   = context.Background()
	start = time.Date(2026, 10, 5, 9, 0, 0, 0, time.UTC)
	locs  = []string{"eu-central-1", "eu-west-2", "us-east-1"}
)

func catalog() *oncall.Index {
	return oncall.NewIndex(oncall.Catalog{
		RotationStart: start,
		Teams: []oncall.Team{
			{ID: "id", Members: []string{"amara", "jonas"}, Manager: "mira"},
			{ID: "shop", Members: []string{"elif", "felix"}, Manager: "greta"},
		},
		Services: []oncall.Service{
			{ID: "auth", Team: "id"},
			{ID: "checkout", Team: "shop", DependsOn: []string{"auth"}},
			{ID: "search", Team: "shop"},
		},
		Components: []oncall.Component{
			{ID: "login", Name: "Login", Public: true},
			{ID: "checkout", Name: "Checkout", Public: true},
			{ID: "search", Name: "Search", Public: true},
		},
		Checks: []oncall.Check{
			{ID: "auth-api", Service: "auth", Component: "login", Locations: locs},
			{ID: "checkout-api", Service: "checkout", Component: "checkout", Locations: locs},
			{ID: "checkout-journey", Service: "checkout", Component: "checkout", Locations: locs},
			{ID: "search-api", Service: "search", Component: "search", Locations: locs},
		},
	})
}

func settings() Settings {
	return Settings{
		ConfirmLocations: 2, ConfirmFailures: 2, ConsecutiveRuns: 1, RegionalRuns: 3, RecoverRuns: 2,
		BlipFilter: true, BlipShare: 0.6, BlipOthersMax: 0.2, BlipMinChecks: 3, BlipHoldMin: 3,
		GroupByService: true, RollUp: true, RollUpWindowMin: 10, ReopenGraceMin: 10, StaleMin: 5,
		Escalation:  []Level{{0, "primary"}, {5, "secondary"}, {12, "manager"}},
		AutoPublish: true, CheckDrafts: true,
	}
}

type pages struct{ got []oncall.Page }

func (p *pages) Notify(pg oncall.Page) { p.got = append(p.got, pg) }

func (p *pages) who() string {
	var s []string
	for _, x := range p.got {
		s = append(s, x.Responder+"/"+x.Level)
	}
	return strings.Join(s, " ")
}

type rig struct {
	t  *testing.T
	d  *Desk
	st *store.Memory
	p  *pages
	m  int
}

func newRig(t *testing.T, s Settings) *rig {
	st := store.NewMemory(0)
	p := &pages{}
	d, err := New(catalog(), st, s, WithNotifier(p))
	if err != nil {
		t.Fatal(err)
	}
	return &rig{t: t, d: d, st: st, p: p}
}

// minute feeds one minute of results; down lists "check@location" that fail, "check" failing everywhere,
// or "@location" failing every check there.
func (r *rig) minute(down ...string) {
	at := start.Add(time.Duration(r.m) * time.Minute)
	for _, c := range r.d.ix.Checks {
		for _, l := range c.Locations {
			ok := true
			for _, x := range down {
				if x == c.ID || x == c.ID+"@"+l || x == "@"+l {
					ok = false
				}
			}
			res := oncall.Result{ID: fmt.Sprintf("%s|%s|%d", c.ID, l, r.m), CheckID: c.ID, Location: l, At: at.Add(10 * time.Second), OK: ok}
			if _, err := r.d.Ingest(ctx, res); err != nil {
				r.t.Fatal(err)
			}
		}
	}
	r.m++
	if err := r.d.Tick(ctx, start.Add(time.Duration(r.m)*time.Minute)); err != nil {
		r.t.Fatal(err)
	}
}

func (r *rig) incidents() []oncall.Incident {
	xs, _ := r.st.Incidents(ctx, false)
	return xs
}

func TestOneFlakeDoesNotPage(t *testing.T) {
	r := newRig(t, settings())
	r.minute("search-api@eu-west-2")
	r.minute()
	r.minute("checkout-api@us-east-1")
	r.minute()
	if len(r.p.got) != 0 || len(r.incidents()) != 0 {
		t.Fatalf("flakes paged: %s", r.p.who())
	}
}

func TestTwoLocationsConfirmAndOneCausePagesOnce(t *testing.T) {
	r := newRig(t, settings())
	r.minute("checkout-api@eu-central-1", "checkout-api@us-east-1")
	if r.p.who() != "elif/primary" {
		t.Fatalf("pages: %s", r.p.who())
	}
	r.minute("checkout-api", "checkout-journey")
	r.minute("checkout-api", "checkout-journey")
	incs := r.incidents()
	if len(incs) != 1 || len(incs[0].Checks) != 2 || incs[0].Severity != "major" {
		t.Fatalf("want one major incident with both checks, got %+v", incs)
	}
	if len(r.p.got) != 1 {
		t.Fatalf("both checks of one service page once, got %s", r.p.who())
	}
}

func TestOneLocationLongEnoughIsRegional(t *testing.T) {
	r := newRig(t, settings())
	r.minute("search-api@us-east-1")
	r.minute("search-api@us-east-1")
	if len(r.p.got) != 0 {
		t.Fatal("two runs at one location are not yet a regional problem")
	}
	r.minute("search-api@us-east-1")
	incs := r.incidents()
	if len(incs) != 1 || incs[0].Severity != "minor" || strings.Join(incs[0].Locations, ",") != "us-east-1" {
		t.Fatalf("want a minor incident in us-east-1, got %+v", incs)
	}
}

func TestLocationBlipIsNotAnIncident(t *testing.T) {
	r := newRig(t, settings())
	for i := 0; i < 5; i++ {
		r.minute("@eu-west-2")
	}
	r.minute()
	if len(r.p.got) != 0 {
		t.Fatalf("a monitoring location losing its network paged %s", r.p.who())
	}
	off := settings()
	off.BlipFilter = false
	r = newRig(t, off)
	for i := 0; i < 5; i++ {
		r.minute("@eu-west-2")
	}
	if len(r.p.got) == 0 {
		t.Fatal("without the filter the blip should page (regional runs), or the test proves nothing")
	}
}

func TestEverywhereFailingIsNotABlip(t *testing.T) {
	r := newRig(t, settings())
	r.minute("@eu-west-2", "@eu-central-1", "@us-east-1")
	if len(r.p.got) == 0 {
		t.Fatal("every location failing is an outage, not a location problem")
	}
}

func TestDependencyBecomesTheRoot(t *testing.T) {
	r := newRig(t, settings())
	r.minute("auth-api", "checkout-api", "checkout-journey")
	r.minute("auth-api", "checkout-api", "checkout-journey")
	if r.p.who() != "amara/primary" {
		t.Fatalf("auth failing takes checkout with it: one page to identity, got %s", r.p.who())
	}
	incs := r.incidents()
	if len(incs) != 2 || incs[1].RootID != incs[0].ID {
		t.Fatalf("checkout should hang under auth: %+v", incs)
	}
	// The dependant opening first is adopted when the dependency follows.
	r = newRig(t, settings())
	r.minute("checkout-api", "checkout-journey")
	r.minute("auth-api", "checkout-api", "checkout-journey")
	incs = r.incidents()
	if len(incs) != 2 || incs[0].RootID != incs[1].ID {
		t.Fatalf("a dependant opened first is adopted by its failing dependency: %+v", incs)
	}
	r.minute("auth-api", "checkout-api", "checkout-journey")
	for i := 0; i < 8; i++ {
		r.minute("auth-api", "checkout-api", "checkout-journey")
	}
	for _, p := range r.p.got {
		if p.IncidentID == incs[0].ID && p.Level != "primary" {
			t.Fatalf("an adopted child must not escalate: %s", r.p.who())
		}
	}
}

func TestChildStillFailingAfterRootRecoversPages(t *testing.T) {
	r := newRig(t, settings())
	r.minute("auth-api", "checkout-api")
	if err := r.d.Ack(ctx, r.incidents()[0].ID, "amara", start.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	r.minute("checkout-api")
	r.minute("checkout-api")
	if strings.Contains(r.p.who(), "elif") {
		t.Fatalf("a dependant gets two minutes to recover after its root: %s", r.p.who())
	}
	for i := 0; i < 3; i++ {
		r.minute("checkout-api")
	}
	if !strings.Contains(r.p.who(), "elif/primary") {
		t.Fatalf("checkout kept failing after auth recovered: its own owner must be paged, got %s", r.p.who())
	}
}

func TestEscalationAndAck(t *testing.T) {
	r := newRig(t, settings())
	for i := 0; i < 14; i++ {
		r.minute("search-api")
	}
	if r.p.who() != "elif/primary felix/secondary greta/manager" {
		t.Fatalf("escalation: %s", r.p.who())
	}
	r = newRig(t, settings())
	r.minute("search-api")
	if err := r.d.Ack(ctx, r.incidents()[0].ID, "elif", start.Add(2*time.Minute)); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 14; i++ {
		r.minute("search-api")
	}
	if r.p.who() != "elif/primary" {
		t.Fatalf("an acknowledged incident does not escalate: %s", r.p.who())
	}
	if err := r.d.Ack(ctx, "inc-999999", "x", start); err == nil {
		t.Fatal("acknowledging an unknown incident must fail")
	}
}

func TestPostsGoOutAndRecoveryResolves(t *testing.T) {
	r := newRig(t, settings())
	r.minute("search-api")
	r.minute("search-api")
	r.minute()
	r.minute()
	incs := r.incidents()
	if incs[0].Open() {
		t.Fatal("two passing runs everywhere resolve the incident")
	}
	posts, _ := r.st.Posts(ctx)
	if len(posts) != 2 || posts[0].Kind != "investigating" || posts[1].Kind != "resolved" {
		t.Fatalf("want investigating then resolved, got %+v", posts)
	}
	if !strings.Contains(posts[0].Text, "Search") || posts[0].At != start.Add(time.Minute) {
		t.Fatalf("first post: %+v", posts[0])
	}
	// Failing again inside the grace reopens without a second page.
	r.minute("search-api")
	if len(r.p.got) != 1 || len(r.incidents()) != 1 || !r.incidents()[0].Open() {
		t.Fatalf("reopen: pages %s, incidents %+v", r.p.who(), r.incidents())
	}
}

func TestDuplicatesAndLateResults(t *testing.T) {
	r := newRig(t, settings())
	res := oncall.Result{ID: "x1", CheckID: "search-api", Location: "us-east-1", At: start.Add(time.Minute), OK: false}
	if fresh, _ := r.d.Ingest(ctx, res); !fresh {
		t.Fatal("first delivery is fresh")
	}
	if fresh, _ := r.d.Ingest(ctx, res); fresh {
		t.Fatal("a duplicate delivery is ignored")
	}
	late := oncall.Result{ID: "x0", CheckID: "search-api", Location: "us-east-1", At: start, OK: true}
	r.d.Ingest(ctx, late)
	if ls := r.d.loc["search-api"]["us-east-1"]; ls.fails != 1 {
		t.Fatalf("a late passing result must not rewind the failure, fails=%d", ls.fails)
	}
	if _, err := r.d.Ingest(ctx, oncall.Result{ID: "y", CheckID: "nope", Location: "us-east-1"}); err == nil {
		t.Fatal("unknown checks are refused")
	}
}

func TestRestartContinuesWithoutRepaging(t *testing.T) {
	r := newRig(t, settings())
	r.minute("search-api")
	r.minute("search-api")
	d2, err := New(catalog(), r.st, settings(), WithNotifier(r.p))
	if err != nil {
		t.Fatal(err)
	}
	if err := d2.Load(ctx, start.Add(2*time.Minute), 30*time.Minute); err != nil {
		t.Fatal(err)
	}
	r.d = d2
	r.minute("search-api")
	r.minute("search-api")
	if r.p.who() != "elif/primary" {
		t.Fatalf("after a restart the open incident continues: %s", r.p.who())
	}
	for i := 0; i < 3; i++ {
		r.minute("search-api")
	}
	if r.p.who() != "elif/primary felix/secondary" {
		t.Fatalf("escalation continues after a restart: %s", r.p.who())
	}
	if incs := r.incidents(); len(incs) != 1 {
		t.Fatalf("no second incident after a restart: %+v", incs)
	}
}

func TestSettingsValidate(t *testing.T) {
	s := settings()
	s.Escalation = []Level{{2, "primary"}}
	if s.Validate() == nil {
		t.Fatal("a policy must start at minute 0")
	}
	s = settings()
	s.RecoverRuns = 0
	if s.Validate() == nil {
		t.Fatal("recover_runs 0 never recovers")
	}
}

func TestPostManualRefusesWhatTheCheckerRefuses(t *testing.T) {
	r := newRig(t, settings())
	r.minute("search-api")
	id := r.incidents()[0].ID
	_, err := r.d.PostManual(ctx, id, "elif", "Search is down because of a bad deploy, back in 10 minutes.", start)
	if _, ok := err.(ErrRejected); !ok {
		t.Fatalf("want a rejection, got %v", err)
	}
	p, err := r.d.PostManual(ctx, id, "elif", "Search is failing for some customers. We are on it.", start)
	if err != nil || p.Author != "elif" {
		t.Fatalf("%+v %v", p, err)
	}
}
