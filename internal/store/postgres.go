package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"time"

	_ "github.com/lib/pq" // the Postgres driver

	"github.com/manojrevenkar12-web/oncall-incident-desk/internal/oncall"
)

// Postgres keeps everything in Postgres 14 or newer. A result is kept once by its primary key.
type Postgres struct {
	db *sql.DB
}

const schema = `
CREATE TABLE IF NOT EXISTS results (
	id          text PRIMARY KEY,
	check_id    text NOT NULL,
	location    text NOT NULL,
	at          timestamptz NOT NULL,
	ok          boolean NOT NULL,
	latency_ms  integer NOT NULL,
	error       text NOT NULL DEFAULT ''
);
CREATE INDEX IF NOT EXISTS results_at ON results (at);
CREATE TABLE IF NOT EXISTS incidents (
	id          text PRIMARY KEY,
	key         text NOT NULL,
	service     text NOT NULL,
	checks      jsonb NOT NULL,
	locations   jsonb NOT NULL,
	severity    text NOT NULL,
	opened_at   timestamptz NOT NULL,
	acked_at    timestamptz,
	acked_by    text NOT NULL DEFAULT '',
	resolved_at timestamptz,
	root_id     text NOT NULL DEFAULT '',
	level       integer NOT NULL
);
CREATE INDEX IF NOT EXISTS incidents_open ON incidents (opened_at) WHERE resolved_at IS NULL;
CREATE TABLE IF NOT EXISTS pages (
	id          text PRIMARY KEY,
	incident_id text NOT NULL REFERENCES incidents (id),
	responder   text NOT NULL,
	level       text NOT NULL,
	at          timestamptz NOT NULL
);
CREATE TABLE IF NOT EXISTS posts (
	id          text PRIMARY KEY,
	incident_id text NOT NULL REFERENCES incidents (id),
	at          timestamptz NOT NULL,
	kind        text NOT NULL,
	components  jsonb NOT NULL,
	text        text NOT NULL,
	author      text NOT NULL,
	checked     boolean NOT NULL,
	fallback    boolean NOT NULL
);`

// OpenPostgres connects and creates the schema if it is missing.
func OpenPostgres(ctx context.Context, dsn string) (*Postgres, error) {
	db, err := sql.Open("postgres", dsn)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(8)
	if err := db.PingContext(ctx); err != nil {
		db.Close()
		return nil, fmt.Errorf("postgres is not reachable at the given DSN: %w", err)
	}
	if _, err := db.ExecContext(ctx, schema); err != nil {
		db.Close()
		return nil, fmt.Errorf("creating the schema: %w", err)
	}
	return &Postgres{db: db}, nil
}

// Reset empties every table (tests and replays only).
func (p *Postgres) Reset(ctx context.Context) error {
	_, err := p.db.ExecContext(ctx, `TRUNCATE posts, pages, incidents, results`)
	return err
}

func (p *Postgres) SaveResult(ctx context.Context, r oncall.Result) (bool, error) {
	res, err := p.db.ExecContext(ctx, `INSERT INTO results (id, check_id, location, at, ok, latency_ms, error)
		VALUES ($1,$2,$3,$4,$5,$6,$7) ON CONFLICT (id) DO NOTHING`,
		r.ID, r.CheckID, r.Location, r.At.UTC(), r.OK, r.LatencyMs, r.Error)
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	return n == 1, err
}

func (p *Postgres) ResultsSince(ctx context.Context, t time.Time) ([]oncall.Result, error) {
	rows, err := p.db.QueryContext(ctx, `SELECT id, check_id, location, at, ok, latency_ms, error
		FROM results WHERE at >= $1 ORDER BY at, id`, t.UTC())
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []oncall.Result
	for rows.Next() {
		var r oncall.Result
		if err := rows.Scan(&r.ID, &r.CheckID, &r.Location, &r.At, &r.OK, &r.LatencyMs, &r.Error); err != nil {
			return nil, err
		}
		r.At = r.At.UTC()
		out = append(out, r)
	}
	return out, rows.Err()
}

func (p *Postgres) PutIncident(ctx context.Context, inc oncall.Incident) error {
	checks, _ := json.Marshal(nonNil(inc.Checks))
	locs, _ := json.Marshal(nonNil(inc.Locations))
	_, err := p.db.ExecContext(ctx, `INSERT INTO incidents
		(id, key, service, checks, locations, severity, opened_at, acked_at, acked_by, resolved_at, root_id, level)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12)
		ON CONFLICT (id) DO UPDATE SET key=EXCLUDED.key, service=EXCLUDED.service, checks=EXCLUDED.checks,
			locations=EXCLUDED.locations, severity=EXCLUDED.severity, opened_at=EXCLUDED.opened_at,
			acked_at=EXCLUDED.acked_at, acked_by=EXCLUDED.acked_by, resolved_at=EXCLUDED.resolved_at,
			root_id=EXCLUDED.root_id, level=EXCLUDED.level`,
		inc.ID, inc.Key, inc.Service, string(checks), string(locs), inc.Severity, inc.OpenedAt.UTC(),
		utcPtr(inc.AckedAt), inc.AckedBy, utcPtr(inc.ResolvedAt), inc.RootID, inc.Level)
	return err
}

const incidentCols = `id, key, service, checks, locations, severity, opened_at, acked_at, acked_by, resolved_at,
	root_id, level`

func scanIncident(sc interface{ Scan(...any) error }) (oncall.Incident, error) {
	var inc oncall.Incident
	var checks, locs []byte
	var acked, resolved sql.NullTime
	if err := sc.Scan(&inc.ID, &inc.Key, &inc.Service, &checks, &locs, &inc.Severity, &inc.OpenedAt, &acked,
		&inc.AckedBy, &resolved, &inc.RootID, &inc.Level); err != nil {
		return inc, err
	}
	if err := json.Unmarshal(checks, &inc.Checks); err != nil {
		return inc, err
	}
	if err := json.Unmarshal(locs, &inc.Locations); err != nil {
		return inc, err
	}
	inc.OpenedAt = inc.OpenedAt.UTC()
	if acked.Valid {
		t := acked.Time.UTC()
		inc.AckedAt = &t
	}
	if resolved.Valid {
		t := resolved.Time.UTC()
		inc.ResolvedAt = &t
	}
	return inc, nil
}

func (p *Postgres) Incident(ctx context.Context, id string) (oncall.Incident, bool, error) {
	row := p.db.QueryRowContext(ctx, `SELECT `+incidentCols+` FROM incidents WHERE id = $1`, id)
	inc, err := scanIncident(row)
	if err == sql.ErrNoRows {
		return oncall.Incident{}, false, nil
	}
	return inc, err == nil, err
}

func (p *Postgres) Incidents(ctx context.Context, openOnly bool) ([]oncall.Incident, error) {
	q := `SELECT ` + incidentCols + ` FROM incidents`
	if openOnly {
		q += ` WHERE resolved_at IS NULL`
	}
	rows, err := p.db.QueryContext(ctx, q+` ORDER BY opened_at, id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []oncall.Incident
	for rows.Next() {
		inc, err := scanIncident(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, inc)
	}
	return out, rows.Err()
}

func (p *Postgres) AddPage(ctx context.Context, pg oncall.Page) error {
	_, err := p.db.ExecContext(ctx, `INSERT INTO pages (id, incident_id, responder, level, at) VALUES ($1,$2,$3,$4,$5)`,
		pg.ID, pg.IncidentID, pg.Responder, pg.Level, pg.At.UTC())
	return err
}

func (p *Postgres) Pages(ctx context.Context) ([]oncall.Page, error) {
	rows, err := p.db.QueryContext(ctx, `SELECT id, incident_id, responder, level, at FROM pages ORDER BY at, id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []oncall.Page
	for rows.Next() {
		var pg oncall.Page
		if err := rows.Scan(&pg.ID, &pg.IncidentID, &pg.Responder, &pg.Level, &pg.At); err != nil {
			return nil, err
		}
		pg.At = pg.At.UTC()
		out = append(out, pg)
	}
	return out, rows.Err()
}

func (p *Postgres) AddPost(ctx context.Context, po oncall.Post) error {
	comps, _ := json.Marshal(nonNil(po.Components))
	_, err := p.db.ExecContext(ctx, `INSERT INTO posts (id, incident_id, at, kind, components, text, author, checked,
		fallback) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9)`,
		po.ID, po.IncidentID, po.At.UTC(), po.Kind, string(comps), po.Text, po.Author, po.Checked, po.Fallback)
	return err
}

func (p *Postgres) Posts(ctx context.Context) ([]oncall.Post, error) {
	rows, err := p.db.QueryContext(ctx, `SELECT id, incident_id, at, kind, components, text, author, checked, fallback
		FROM posts ORDER BY at, id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []oncall.Post
	for rows.Next() {
		var po oncall.Post
		var comps []byte
		if err := rows.Scan(&po.ID, &po.IncidentID, &po.At, &po.Kind, &comps, &po.Text, &po.Author, &po.Checked,
			&po.Fallback); err != nil {
			return nil, err
		}
		if err := json.Unmarshal(comps, &po.Components); err != nil {
			return nil, err
		}
		po.At = po.At.UTC()
		out = append(out, po)
	}
	return out, rows.Err()
}

func (p *Postgres) Close() error { return p.db.Close() }

func nonNil(xs []string) []string {
	if xs == nil {
		return []string{}
	}
	return xs
}

func utcPtr(t *time.Time) any {
	if t == nil {
		return nil
	}
	return t.UTC()
}
