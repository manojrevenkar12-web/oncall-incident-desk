// Package oncall is the shared vocabulary: checks and their results, the services and components they watch,
// the teams that carry the pager, incidents, pages and status-page posts. It imports only the standard library.
package oncall

import (
	"sort"
	"time"
)

// Result is one run of one check from one location, as a monitoring webhook delivers it.
type Result struct {
	ID        string    `json:"id"`
	CheckID   string    `json:"check_id"`
	Location  string    `json:"location"`
	At        time.Time `json:"at"`
	OK        bool      `json:"ok"`
	LatencyMs int       `json:"latency_ms"`
	Error     string    `json:"error,omitempty"`
}

// Check is an API or browser check on one component of one service, run from several locations.
type Check struct {
	ID        string   `json:"id"`
	Name      string   `json:"name"`
	Service   string   `json:"service"`
	Component string   `json:"component"`
	Locations []string `json:"locations"`
}

// Component is what a customer sees on the status page. Internal components never appear there.
type Component struct {
	ID     string `json:"id"`
	Name   string `json:"name"`
	Public bool   `json:"public"`
}

// Service is a deployable thing with an owning team. DependsOn names services whose failure breaks this one.
type Service struct {
	ID        string   `json:"id"`
	Team      string   `json:"team"`
	DependsOn []string `json:"depends_on,omitempty"`
	// Hosts are internal names that must never reach a public post.
	Hosts []string `json:"hosts,omitempty"`
}

// Team carries the pager for its services. Members rotate weekly: in week w the primary is
// Members[w mod n] and the secondary Members[(w+1) mod n]. The manager is the last escalation level.
type Team struct {
	ID      string   `json:"id"`
	Members []string `json:"members"`
	Manager string   `json:"manager"`
}

// Catalog is the customer's monitoring-as-code configuration: what is checked, who owns it and what is public.
type Catalog struct {
	Services   []Service   `json:"services"`
	Components []Component `json:"components"`
	Checks     []Check     `json:"checks"`
	Teams      []Team      `json:"teams"`
	// RotationStart is the Monday the week numbering starts from.
	RotationStart time.Time `json:"rotation_start"`
}

// Index answers catalogue lookups in constant time.
type Index struct {
	Catalog
	Service   map[string]Service
	Component map[string]Component
	Check     map[string]Check
	Team      map[string]Team
	// ChecksOf lists check IDs per service, sorted.
	ChecksOf map[string][]string
	// Downstream lists the services that depend on a service, sorted.
	Downstream map[string][]string
	// Order is the services with every dependency before its dependants (ties by ID).
	Order []string
}

// NewIndex builds the lookups. Unknown references are kept out of the maps; Validate reports them.
func NewIndex(c Catalog) *Index {
	ix := &Index{
		Catalog:    c,
		Service:    map[string]Service{},
		Component:  map[string]Component{},
		Check:      map[string]Check{},
		Team:       map[string]Team{},
		ChecksOf:   map[string][]string{},
		Downstream: map[string][]string{},
	}
	for _, s := range c.Services {
		ix.Service[s.ID] = s
	}
	for _, p := range c.Components {
		ix.Component[p.ID] = p
	}
	for _, t := range c.Teams {
		ix.Team[t.ID] = t
	}
	for _, ch := range c.Checks {
		ix.Check[ch.ID] = ch
		ix.ChecksOf[ch.Service] = append(ix.ChecksOf[ch.Service], ch.ID)
	}
	for _, s := range c.Services {
		for _, d := range s.DependsOn {
			ix.Downstream[d] = append(ix.Downstream[d], s.ID)
		}
	}
	for k := range ix.ChecksOf {
		sort.Strings(ix.ChecksOf[k])
	}
	for k := range ix.Downstream {
		sort.Strings(ix.Downstream[k])
	}
	ix.Order = topo(c.Services)
	return ix
}

func topo(services []Service) []string {
	ids := make([]string, 0, len(services))
	deps := map[string][]string{}
	for _, s := range services {
		ids = append(ids, s.ID)
		deps[s.ID] = s.DependsOn
	}
	sort.Strings(ids)
	seen := map[string]int{} // 1 visiting, 2 done
	var out []string
	var visit func(string)
	visit = func(id string) {
		if seen[id] != 0 {
			return // done, or a cycle: Validate reports cycles
		}
		seen[id] = 1
		d := append([]string(nil), deps[id]...)
		sort.Strings(d)
		for _, x := range d {
			if _, ok := deps[x]; ok {
				visit(x)
			}
		}
		seen[id] = 2
		out = append(out, id)
	}
	for _, id := range ids {
		visit(id)
	}
	return out
}

// OnCall returns who holds a level ("primary", "secondary", "manager") for a team at a moment.
func (ix *Index) OnCall(team, level string, at time.Time) string {
	t, ok := ix.Team[team]
	if !ok || len(t.Members) == 0 {
		return ""
	}
	week := int(at.Sub(ix.RotationStart).Hours() / (24 * 7))
	if week < 0 {
		week = 0
	}
	n := len(t.Members)
	switch level {
	case "primary":
		return t.Members[week%n]
	case "secondary":
		return t.Members[(week+1)%n]
	case "manager":
		return t.Manager
	}
	return ""
}

// Incident groups failing checks that share a cause. A child incident was merged into a root.
type Incident struct {
	ID         string     `json:"id"`
	Key        string     `json:"key"` // the service, or the check when grouping is off
	Service    string     `json:"service"`
	Checks     []string   `json:"checks"`
	Locations  []string   `json:"locations"`
	Severity   string     `json:"severity"` // "major" or "minor" (one region)
	OpenedAt   time.Time  `json:"opened_at"`
	AckedAt    *time.Time `json:"acked_at,omitempty"`
	AckedBy    string     `json:"acked_by,omitempty"`
	ResolvedAt *time.Time `json:"resolved_at,omitempty"`
	RootID     string     `json:"root_id,omitempty"` // set on a child
	Level      int        `json:"level"`             // the escalation level paged last
}

// Open reports whether the incident is unresolved.
func (i *Incident) Open() bool { return i.ResolvedAt == nil }

// Page is one notification to one person about one incident.
type Page struct {
	ID         string    `json:"id"`
	IncidentID string    `json:"incident_id"`
	Responder  string    `json:"responder"`
	Level      string    `json:"level"`
	At         time.Time `json:"at"`
}

// Post is one status-page update.
type Post struct {
	ID         string    `json:"id"`
	IncidentID string    `json:"incident_id"`
	At         time.Time `json:"at"`
	Kind       string    `json:"kind"` // "investigating" or "resolved"
	Components []string  `json:"components"`
	Text       string    `json:"text"`
	Author     string    `json:"author"` // "desk", "model" or a responder
	// Checked tells whether the text passed the claim checker; Fallback that the checker replaced it.
	Checked  bool `json:"checked"`
	Fallback bool `json:"fallback"`
}

// Night reports whether a moment is outside 07:00-22:00 Berlin time (UTC+1 in winter, as the lab assumes).
func Night(at time.Time) bool {
	h := at.UTC().Add(time.Hour).Hour()
	return h < 7 || h >= 22
}
