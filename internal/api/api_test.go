package api

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/manojrevenkar12-web/oncall-incident-desk/internal/desk"
	"github.com/manojrevenkar12-web/oncall-incident-desk/internal/oncall"
	"github.com/manojrevenkar12-web/oncall-incident-desk/internal/store"
)

var t0 = time.Date(2026, 10, 5, 9, 0, 0, 0, time.UTC)

func server(t *testing.T) (*Server, *httptest.Server) {
	ix := oncall.NewIndex(oncall.Catalog{
		RotationStart: t0,
		Teams:         []oncall.Team{{ID: "shop", Members: []string{"elif", "felix"}, Manager: "greta"}},
		Services:      []oncall.Service{{ID: "search", Team: "shop"}},
		Components:    []oncall.Component{{ID: "search", Name: "Search", Public: true}},
		Checks:        []oncall.Check{{ID: "search-api", Service: "search", Component: "search", Locations: []string{"a", "b"}}},
	})
	st := store.NewMemory(0)
	d, err := desk.New(ix, st, desk.Settings{ConfirmLocations: 2, ConsecutiveRuns: 1, RecoverRuns: 2, GroupByService: true,
		Escalation: []desk.Level{{AfterMin: 0, Target: "primary"}}, AutoPublish: true, CheckDrafts: true})
	if err != nil {
		t.Fatal(err)
	}
	now := t0
	s := &Server{Desk: d, Store: st, Index: ix, Secret: []byte("s3cret"), Now: func() time.Time { return now }}
	ts := httptest.NewServer(s.Handler())
	t.Cleanup(ts.Close)
	return s, ts
}

func send(t *testing.T, ts *httptest.Server, path string, body any, sign []byte) (*http.Response, map[string]any) {
	b, _ := json.Marshal(body)
	req, _ := http.NewRequest(http.MethodPost, ts.URL+path, bytes.NewReader(b))
	if sign != nil {
		req.Header.Set("X-Desk-Signature", Sign(sign, b))
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var out map[string]any
	json.NewDecoder(resp.Body).Decode(&out)
	return resp, out
}

func batch(ok bool, m int) map[string]any {
	var rs []oncall.Result
	for _, l := range []string{"a", "b"} {
		rs = append(rs, oncall.Result{ID: fmt.Sprintf("%s-%d", l, m), CheckID: "search-api", Location: l,
			At: t0.Add(time.Duration(m)*time.Minute + time.Second), OK: ok})
	}
	return map[string]any{"results": rs}
}

func TestWebhookIsSignedAndIdempotent(t *testing.T) {
	_, ts := server(t)
	if resp, _ := send(t, ts, "/v1/results", batch(false, 0), nil); resp.StatusCode != 401 {
		t.Fatalf("unsigned: %d", resp.StatusCode)
	}
	if resp, _ := send(t, ts, "/v1/results", batch(false, 0), []byte("wrong")); resp.StatusCode != 401 {
		t.Fatalf("wrong secret: %d", resp.StatusCode)
	}
	resp, out := send(t, ts, "/v1/results", batch(false, 0), []byte("s3cret"))
	if resp.StatusCode != 202 || out["accepted"].(float64) != 2 {
		t.Fatalf("%d %v", resp.StatusCode, out)
	}
	_, out = send(t, ts, "/v1/results", batch(false, 0), []byte("s3cret"))
	if out["duplicates"].(float64) != 2 {
		t.Fatalf("a retried delivery is a duplicate: %v", out)
	}
	bad := map[string]any{"results": []oncall.Result{{ID: "x", CheckID: "nope", Location: "a"}}}
	if resp, _ := send(t, ts, "/v1/results", bad, []byte("s3cret")); resp.StatusCode != 422 {
		t.Fatalf("unknown check: %d", resp.StatusCode)
	}
}

func TestIncidentAckPostAndStatusPage(t *testing.T) {
	s, ts := server(t)
	send(t, ts, "/v1/results", batch(false, 0), []byte("s3cret"))
	s.Desk.Tick(context.Background(), t0.Add(time.Minute))
	resp, err := http.Get(ts.URL + "/v1/incidents?open=true")
	if err != nil {
		t.Fatal(err)
	}
	var incs []oncall.Incident
	json.NewDecoder(resp.Body).Decode(&incs)
	if len(incs) != 1 {
		t.Fatalf("want one open incident, got %+v", incs)
	}
	id := incs[0].ID
	if resp, _ := send(t, ts, "/v1/incidents/"+id+"/ack", map[string]string{"responder": "elif"}, nil); resp.StatusCode != 200 {
		t.Fatalf("ack: %d", resp.StatusCode)
	}
	if resp, _ := send(t, ts, "/v1/incidents/nope/ack", map[string]string{"responder": "elif"}, nil); resp.StatusCode != 404 {
		t.Fatalf("unknown ack: %d", resp.StatusCode)
	}
	resp2, out := send(t, ts, "/v1/incidents/"+id+"/posts", map[string]string{"author": "elif", "text": "Search is down due to a bad deploy."}, nil)
	if resp2.StatusCode != 422 || out["violations"] == nil {
		t.Fatalf("a cause claim is refused with reasons: %d %v", resp2.StatusCode, out)
	}
	var st Status
	r3, _ := http.Get(ts.URL + "/v1/status")
	json.NewDecoder(r3.Body).Decode(&st)
	if len(st.Components) != 1 || st.Components[0].Status != "outage" || len(st.Posts) != 1 {
		t.Fatalf("status: %+v", st)
	}
	r4, _ := http.Get(ts.URL + "/status")
	var b bytes.Buffer
	b.ReadFrom(r4.Body)
	if !strings.Contains(b.String(), "Not working") || !strings.Contains(b.String(), "We are investigating problems") &&
		!strings.Contains(b.String(), "investigating failures affecting Search") {
		t.Fatalf("html: %s", b.String())
	}
}
