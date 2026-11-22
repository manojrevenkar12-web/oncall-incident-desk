package drafts

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/manojrevenkar12-web/oncall-incident-desk/internal/oncall"
)

func index() *oncall.Index {
	return oncall.NewIndex(oncall.Catalog{
		Services: []oncall.Service{{ID: "checkout", Team: "t", Hosts: []string{"checkout-svc.prod.internal"}}},
		Components: []oncall.Component{
			{ID: "checkout", Name: "Checkout", Public: true},
			{ID: "search", Name: "Search", Public: true},
			{ID: "reporting", Name: "Reporting jobs"},
		},
	})
}

func facts() Facts {
	return Facts{IncidentID: "inc-1", Kind: "investigating", Started: time.Date(2026, 10, 5, 14, 2, 0, 0, time.UTC),
		Components: []oncall.Component{{ID: "checkout", Name: "Checkout", Public: true}}}
}

func kinds(vs []Violation) string {
	var ks []string
	for _, v := range vs {
		ks = append(ks, v.Kind)
	}
	return strings.Join(ks, ",")
}

func TestTemplatePassesItsOwnChecker(t *testing.T) {
	c := NewChecker(index())
	for _, f := range []Facts{facts(), func() Facts { f := facts(); f.Kind = "resolved"; return f }(),
		func() Facts { f := facts(); f.Regions = []string{"eu-central-1"}; return f }()} {
		if v := c.Check(Write(f), f); len(v) > 0 {
			t.Fatalf("template %q fails: %v", Write(f), v)
		}
	}
}

func TestCheckerCatchesEachCategory(t *testing.T) {
	c := NewChecker(index())
	cases := map[string]string{
		"Checkout and Search are failing.":                                 UnaffectedComponent,
		"Something is wrong.":                                              MissingComponent,
		"Checkout is failing. We expect a fix within 30 minutes.":          RecoveryPromise,
		"Checkout is failing. Give us twenty minutes.":                     RecoveryPromise,
		"Checkout is failing. We should have it fixed before lunch.":       RecoveryPromise,
		"Checkout is failing. Back at 16:00.":                              RecoveryPromise,
		"Checkout is failing. A database failover is behind this.":         UnconfirmedCause,
		"Checkout is failing because of a bad deploy.":                     UnconfirmedCause,
		"Checkout is failing. checkout-svc.prod.internal is down.":         InternalDetail,
		"Checkout is failing. The primary db-main-2 stopped answering.":    InternalDetail,
		"Checkout is failing. Health checks on 10.20.4.17 are timing out.": InternalDetail,
		"": Shape,
	}
	for text, want := range cases {
		got := kinds(c.Check(text, facts()))
		if !strings.Contains(got, want) {
			t.Errorf("%q: got %q, want %s", text, got, want)
		}
	}
	if v := c.Check("We are investigating problems with Checkout. Engineers are working on it.", facts()); len(v) > 0 {
		t.Fatalf("a clean draft was rejected: %v", v)
	}
	if v := c.Check("Reporting jobs are late. Checkout is failing.", facts()); len(v) > 0 {
		t.Fatalf("an internal component is not on the status page, so naming it is not a public claim: %v", v)
	}
}

type fixed struct {
	text string
	err  error
}

func (f fixed) Draft(context.Context, Facts) (string, error) { return f.text, f.err }

func TestDecideFallsBackToTheTemplate(t *testing.T) {
	c := NewChecker(index())
	f := facts()
	text, v, fb := Decide(context.Background(), fixed{text: "Checkout is down due to a bad release."}, c, f, true)
	if !fb || text != Write(f) || len(v) == 0 {
		t.Fatalf("got %q %v %v", text, v, fb)
	}
	text, _, fb = Decide(context.Background(), fixed{text: "Checkout is down due to a bad release."}, c, f, false)
	if fb || !strings.Contains(text, "bad release") {
		t.Fatalf("with checking off the draft goes out as written, got %q", text)
	}
	text, _, fb = Decide(context.Background(), fixed{err: errors.New("timeout")}, c, f, true)
	if !fb || text != Write(f) {
		t.Fatalf("a drafter error publishes the template, got %q", text)
	}
}

func TestClaudeDrafterSpeaksTheMessagesAPI(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/messages" || r.Header.Get("x-api-key") != "k" || r.Header.Get("anthropic-version") == "" {
			http.Error(w, "bad request", 400)
			return
		}
		w.Write([]byte(`{"content":[{"type":"text","text":"  We are investigating problems with Checkout.  "}]}`))
	}))
	defer srv.Close()
	got, err := Claude{APIKey: "k", BaseURL: srv.URL}.Draft(context.Background(), facts())
	if err != nil || got != "We are investigating problems with Checkout." {
		t.Fatalf("got %q, %v", got, err)
	}
	if _, err := (Claude{}).Draft(context.Background(), facts()); err == nil {
		t.Fatal("no key must be an error")
	}
}
