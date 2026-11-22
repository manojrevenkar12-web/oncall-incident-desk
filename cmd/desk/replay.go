package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"reflect"
	"time"

	"github.com/manojrevenkar12-web/oncall-incident-desk/internal/lab"
	"github.com/manojrevenkar12-web/oncall-incident-desk/internal/store"
	"github.com/manojrevenkar12-web/oncall-incident-desk/internal/world"
)

// replay plays the desk design over the first hours of a dev seed twice, into Postgres and into memory, and
// checks that both produced the same incidents, pages and posts: the store never changes a decision.
func replay(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("replay", flag.ExitOnError)
	dsn := fs.String("dsn", os.Getenv("DATABASE_URL"), "Postgres DSN (the tables are emptied)")

	seed := fs.Uint64("seed", 1, "dev seed")
	fs.Parse(args)
	if *dsn == "" {
		return fmt.Errorf("replay needs -dsn or DATABASE_URL")
	}
	designs, _, err := lab.LoadDesigns(".")
	if err != nil {
		return err
	}
	var d lab.Design
	for _, x := range designs {
		if x.Name == "desk" {
			d = x
		}
	}
	cfg, err := world.Load("config/world.json")
	if err != nil {
		return err
	}
	ph, err := world.LoadPhrases(cfg.Model.PhrasesFile)
	if err != nil {
		return err
	}
	pg, err := store.OpenPostgres(ctx, *dsn)
	if err != nil {
		return err
	}
	defer pg.Close()
	if err := pg.Reset(ctx); err != nil {
		return err
	}
	from, to := lab.Window(cfg, ph, *seed)
	began := time.Now()
	a, err := lab.PlayInto(ctx, d, cfg, ph, *seed, pg, from, to)
	if err != nil {
		return err
	}
	took := time.Since(began)
	b, err := lab.PlayInto(ctx, d, cfg, ph, *seed, store.NewMemory(2*time.Hour), from, to)
	if err != nil {
		return err
	}
	same := func(x, y any) bool {
		jx, _ := json.Marshal(x)
		jy, _ := json.Marshal(y)
		if string(jx) == "null" {
			jx = []byte("[]")
		}
		if string(jy) == "null" {
			jy = []byte("[]")
		}
		return reflect.DeepEqual(jx, jy)
	}
	hours := (to - from) / 60
	results := len(cfg.Locations) * (to - from)
	n := 0
	for _, s := range cfg.Services {
		n += len(s.Checks)
	}
	results *= n
	fmt.Printf("replayed %d hours around the showcase outage (%d results) into Postgres in %s\n", hours, results, took.Round(time.Millisecond))
	fmt.Printf("incidents %d, pages %d, posts %d\n", len(a.Incidents), len(a.Pages), len(a.Posts))
	ok := same(a.Incidents, b.Incidents) && same(a.Pages, b.Pages) && same(a.Posts, b.Posts)
	fmt.Printf("postgres and memory agree: %v\n", ok)
	if !ok && os.Getenv("DESK_REPLAY_DEBUG") != "" {
		for i := range a.Incidents {
			ja, _ := json.Marshal(a.Incidents[i])
			jb, _ := json.Marshal(b.Incidents[i])
			if string(ja) != string(jb) {
				fmt.Printf("pg  %s\nmem %s\n", ja, jb)
			}
		}
	}
	if !ok {
		return fmt.Errorf("the store changed a decision")
	}
	return nil
}
