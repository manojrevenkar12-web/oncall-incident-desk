package store

import (
	"context"
	"os"
	"reflect"
	"sync"
	"testing"
	"time"

	"github.com/manojrevenkar12-web/oncall-incident-desk/internal/oncall"
)

// stores returns every implementation to run the contract against. Postgres joins when DESK_TEST_DATABASE_URL
// is set (CI sets it on a postgres:16 service).
func stores(t *testing.T) map[string]Store {
	out := map[string]Store{"memory": NewMemory(0)}
	if dsn := os.Getenv("DESK_TEST_DATABASE_URL"); dsn != "" {
		pg, err := OpenPostgres(context.Background(), dsn)
		if err != nil {
			t.Fatal(err)
		}
		if err := pg.Reset(context.Background()); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { pg.Close() })
		out["postgres"] = pg
	}
	return out
}

var t0 = time.Date(2026, 10, 5, 9, 0, 0, 0, time.UTC)

func TestContract(t *testing.T) {
	ctx := context.Background()
	for name, st := range stores(t) {
		t.Run(name, func(t *testing.T) {
			r := oncall.Result{ID: "r1", CheckID: "c", Location: "eu-west-2", At: t0, OK: false, LatencyMs: 30000, Error: "timeout"}
			fresh, err := st.SaveResult(ctx, r)
			if err != nil || !fresh {
				t.Fatalf("first save: %v %v", fresh, err)
			}
			if fresh, _ := st.SaveResult(ctx, r); fresh {
				t.Fatal("a result is kept once")
			}
			st.SaveResult(ctx, oncall.Result{ID: "r0", CheckID: "c", Location: "eu-west-2", At: t0.Add(-time.Hour), OK: true})
			rs, err := st.ResultsSince(ctx, t0.Add(-time.Minute))
			if err != nil || len(rs) != 1 || !reflect.DeepEqual(rs[0], r) {
				t.Fatalf("results since: %+v %v", rs, err)
			}

			acked := t0.Add(3 * time.Minute)
			inc := oncall.Incident{ID: "inc-000001", Key: "search", Service: "search", Checks: []string{"a", "b"},
				Locations: []string{"us-east-1"}, Severity: "minor", OpenedAt: t0, AckedAt: &acked, AckedBy: "elif", Level: 1}
			if err := st.PutIncident(ctx, inc); err != nil {
				t.Fatal(err)
			}
			got, ok, err := st.Incident(ctx, inc.ID)
			if err != nil || !ok || !reflect.DeepEqual(got, inc) {
				t.Fatalf("incident round trip:\n got %+v\nwant %+v (%v)", got, inc, err)
			}
			resolved := t0.Add(20 * time.Minute)
			inc.ResolvedAt = &resolved
			inc.RootID = "inc-000000"
			if err := st.PutIncident(ctx, inc); err != nil {
				t.Fatal(err)
			}
			st.PutIncident(ctx, oncall.Incident{ID: "inc-000002", Key: "auth", Service: "auth", Checks: []string{"x"},
				Severity: "major", OpenedAt: t0.Add(time.Minute)})
			open, _ := st.Incidents(ctx, true)
			all, _ := st.Incidents(ctx, false)
			if len(open) != 1 || open[0].ID != "inc-000002" || len(all) != 2 || all[0].ResolvedAt == nil {
				t.Fatalf("open %+v all %+v", open, all)
			}
			if open[0].Locations == nil {
				open[0].Locations = []string{}
			}
			if _, ok, _ := st.Incident(ctx, "nope"); ok {
				t.Fatal("unknown incident found")
			}

			p := oncall.Page{ID: "pg-000003", IncidentID: "inc-000001", Responder: "elif", Level: "primary", At: t0}
			if err := st.AddPage(ctx, p); err != nil {
				t.Fatal(err)
			}
			ps, _ := st.Pages(ctx)
			if len(ps) != 1 || !reflect.DeepEqual(ps[0], p) {
				t.Fatalf("pages %+v", ps)
			}
			po := oncall.Post{ID: "po-000004", IncidentID: "inc-000001", At: t0, Kind: "investigating",
				Components: []string{"search"}, Text: "Search is failing.", Author: "desk", Checked: true}
			if err := st.AddPost(ctx, po); err != nil {
				t.Fatal(err)
			}
			pos, _ := st.Posts(ctx)
			if len(pos) != 1 || !reflect.DeepEqual(pos[0], po) {
				t.Fatalf("posts %+v", pos)
			}
		})
	}
}

// A webhook retried while the first delivery is still being written must still be kept once.
func TestConcurrentDuplicatesKeptOnce(t *testing.T) {
	ctx := context.Background()
	for name, st := range stores(t) {
		t.Run(name, func(t *testing.T) {
			var wg sync.WaitGroup
			var mu sync.Mutex
			fresh := 0
			for i := 0; i < 20; i++ {
				wg.Add(1)
				go func() {
					defer wg.Done()
					ok, err := st.SaveResult(ctx, oncall.Result{ID: "dup", CheckID: "c", Location: "l", At: t0})
					if err != nil {
						t.Error(err)
					}
					if ok {
						mu.Lock()
						fresh++
						mu.Unlock()
					}
				}()
			}
			wg.Wait()
			if fresh != 1 {
				t.Fatalf("%d deliveries were taken as fresh", fresh)
			}
		})
	}
}

func TestMemoryRetention(t *testing.T) {
	ctx := context.Background()
	m := NewMemory(time.Hour)
	for i := 0; i < 300; i++ {
		m.SaveResult(ctx, oncall.Result{ID: string(rune('a'+i%26)) + time.Duration(i).String(), At: t0.Add(time.Duration(i) * time.Minute)})
	}
	rs, _ := m.ResultsSince(ctx, time.Time{})
	if len(rs) > 80 {
		t.Fatalf("kept %d results with an hour's retention", len(rs))
	}
}
