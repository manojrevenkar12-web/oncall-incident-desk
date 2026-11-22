// Package store keeps what the desk has seen and done: check results (each kept once), incidents, pages and
// status posts. Postgres and memory implement one contract (store_test.go runs it against both).
package store

import (
	"context"
	"sort"
	"time"

	"github.com/manojrevenkar12-web/oncall-incident-desk/internal/oncall"
)

// Store is the desk's persistence contract.
type Store interface {
	// SaveResult keeps a result once. It returns false, and changes nothing, when the ID was seen before.
	SaveResult(ctx context.Context, r oncall.Result) (bool, error)
	// ResultsSince returns results at or after t, oldest first (ties by ID).
	ResultsSince(ctx context.Context, t time.Time) ([]oncall.Result, error)
	// PutIncident inserts or replaces an incident by ID.
	PutIncident(ctx context.Context, inc oncall.Incident) error
	Incident(ctx context.Context, id string) (oncall.Incident, bool, error)
	// Incidents returns incidents by OpenedAt then ID; openOnly drops resolved ones.
	Incidents(ctx context.Context, openOnly bool) ([]oncall.Incident, error)
	AddPage(ctx context.Context, p oncall.Page) error
	Pages(ctx context.Context) ([]oncall.Page, error)
	AddPost(ctx context.Context, p oncall.Post) error
	Posts(ctx context.Context) ([]oncall.Post, error)
	Close() error
}

func sortResults(rs []oncall.Result) {
	sort.Slice(rs, func(i, j int) bool {
		if !rs[i].At.Equal(rs[j].At) {
			return rs[i].At.Before(rs[j].At)
		}
		return rs[i].ID < rs[j].ID
	})
}

func sortIncidents(xs []oncall.Incident) {
	sort.Slice(xs, func(i, j int) bool {
		if !xs[i].OpenedAt.Equal(xs[j].OpenedAt) {
			return xs[i].OpenedAt.Before(xs[j].OpenedAt)
		}
		return xs[i].ID < xs[j].ID
	})
}

func sortPages(xs []oncall.Page) {
	sort.Slice(xs, func(i, j int) bool {
		if !xs[i].At.Equal(xs[j].At) {
			return xs[i].At.Before(xs[j].At)
		}
		return xs[i].ID < xs[j].ID
	})
}

func sortPosts(xs []oncall.Post) {
	sort.Slice(xs, func(i, j int) bool {
		if !xs[i].At.Equal(xs[j].At) {
			return xs[i].At.Before(xs[j].At)
		}
		return xs[i].ID < xs[j].ID
	})
}
