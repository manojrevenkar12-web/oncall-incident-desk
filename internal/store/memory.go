package store

import (
	"context"
	"sync"
	"time"

	"github.com/manojrevenkar12-web/oncall-incident-desk/internal/oncall"
)

// Memory is the in-process store the lab uses. It keeps results for Retention (de-duplication included), and
// incidents, pages and posts for ever.
type Memory struct {
	mu        sync.Mutex
	Retention time.Duration
	seen      map[string]time.Time
	results   []oncall.Result
	newest    time.Time
	pruned    time.Time
	incidents map[string]oncall.Incident
	pages     []oncall.Page
	posts     []oncall.Post
}

// NewMemory returns an empty store keeping results for retention (zero keeps them all).
func NewMemory(retention time.Duration) *Memory {
	return &Memory{Retention: retention, seen: map[string]time.Time{}, incidents: map[string]oncall.Incident{}}
}

func (m *Memory) SaveResult(_ context.Context, r oncall.Result) (bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.seen[r.ID]; ok {
		return false, nil
	}
	r.At = r.At.UTC()
	m.seen[r.ID] = r.At
	m.results = append(m.results, r)
	if r.At.After(m.newest) {
		m.newest = r.At
	}
	if m.Retention > 0 && m.newest.Sub(m.pruned) > m.Retention/4 {
		m.prune()
	}
	return true, nil
}

func (m *Memory) prune() {
	cut := m.newest.Add(-m.Retention)
	kept := m.results[:0]
	for _, r := range m.results {
		if !r.At.Before(cut) {
			kept = append(kept, r)
		} else {
			delete(m.seen, r.ID)
		}
	}
	m.results = kept
	m.pruned = m.newest
}

func (m *Memory) ResultsSince(_ context.Context, t time.Time) ([]oncall.Result, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []oncall.Result
	for _, r := range m.results {
		if !r.At.Before(t) {
			out = append(out, r)
		}
	}
	sortResults(out)
	return out, nil
}

func (m *Memory) PutIncident(_ context.Context, inc oncall.Incident) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.incidents[inc.ID] = clone(inc)
	return nil
}

func (m *Memory) Incident(_ context.Context, id string) (oncall.Incident, bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	inc, ok := m.incidents[id]
	return clone(inc), ok, nil
}

func (m *Memory) Incidents(_ context.Context, openOnly bool) ([]oncall.Incident, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]oncall.Incident, 0, len(m.incidents))
	for _, inc := range m.incidents {
		if openOnly && !inc.Open() {
			continue
		}
		out = append(out, clone(inc))
	}
	sortIncidents(out)
	return out, nil
}

func (m *Memory) AddPage(_ context.Context, p oncall.Page) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	p.At = p.At.UTC()
	m.pages = append(m.pages, p)
	return nil
}

func (m *Memory) Pages(context.Context) ([]oncall.Page, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := append([]oncall.Page(nil), m.pages...)
	sortPages(out)
	return out, nil
}

func (m *Memory) AddPost(_ context.Context, p oncall.Post) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	p.At = p.At.UTC()
	p.Components = append([]string(nil), p.Components...)
	m.posts = append(m.posts, p)
	return nil
}

func (m *Memory) Posts(context.Context) ([]oncall.Post, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := append([]oncall.Post(nil), m.posts...)
	sortPosts(out)
	return out, nil
}

func (m *Memory) Close() error { return nil }

func clone(inc oncall.Incident) oncall.Incident {
	inc.Checks = append([]string(nil), inc.Checks...)
	inc.Locations = append([]string(nil), inc.Locations...)
	inc.OpenedAt = inc.OpenedAt.UTC()
	if inc.AckedAt != nil {
		t := inc.AckedAt.UTC()
		inc.AckedAt = &t
	}
	if inc.ResolvedAt != nil {
		t := inc.ResolvedAt.UTC()
		inc.ResolvedAt = &t
	}
	return inc
}
