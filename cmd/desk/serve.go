package main

import (
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"time"

	"github.com/manojrevenkar12-web/oncall-incident-desk/internal/api"
	"github.com/manojrevenkar12-web/oncall-incident-desk/internal/desk"
	"github.com/manojrevenkar12-web/oncall-incident-desk/internal/drafts"
	"github.com/manojrevenkar12-web/oncall-incident-desk/internal/oncall"
	"github.com/manojrevenkar12-web/oncall-incident-desk/internal/store"
)

// pager logs every page and, when DESK_PAGE_WEBHOOK is set, posts it there (Slack-compatible {"text": ...}).
type pager struct{ url string }

func (p pager) Notify(pg oncall.Page) {
	msg := fmt.Sprintf("PAGE %s (%s) for %s at %s", pg.Responder, pg.Level, pg.IncidentID, pg.At.Format(time.RFC3339))
	log.Print(msg)
	if p.url == "" {
		return
	}
	body, _ := json.Marshal(map[string]string{"text": msg})
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		req, _ := http.NewRequestWithContext(ctx, http.MethodPost, p.url, bytes.NewReader(body))
		req.Header.Set("content-type", "application/json")
		if resp, err := http.DefaultClient.Do(req); err != nil {
			log.Printf("page webhook failed: %v", err)
		} else {
			resp.Body.Close()
		}
	}()
}

func readFile(path string, v any) error {
	b, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	return json.Unmarshal(b, v)
}

func serve(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("serve", flag.ExitOnError)
	addr := fs.String("addr", ":8080", "listen address")
	dsn := fs.String("dsn", os.Getenv("DATABASE_URL"), "Postgres DSN; empty keeps everything in memory")
	catPath := fs.String("catalog", "config/catalog.example.json", "checks, components, services and teams")
	setPath := fs.String("settings", "config/desk.json", "the desk's rules")
	fs.Parse(args)
	var cat oncall.Catalog
	if err := readFile(*catPath, &cat); err != nil {
		return fmt.Errorf("catalogue: %w", err)
	}
	var set desk.Settings
	if err := readFile(*setPath, &set); err != nil {
		return fmt.Errorf("settings: %w", err)
	}
	var st store.Store = store.NewMemory(6 * time.Hour)
	if *dsn != "" {
		pg, err := store.OpenPostgres(ctx, *dsn)
		if err != nil {
			return err
		}
		defer pg.Close()
		st = pg
	}
	ix := oncall.NewIndex(cat)
	opts := []desk.Option{desk.WithNotifier(pager{os.Getenv("DESK_PAGE_WEBHOOK")})}
	if key := os.Getenv("ANTHROPIC_API_KEY"); key != "" {
		opts = append(opts, desk.WithDrafter(drafts.Claude{APIKey: key, Model: os.Getenv("DESK_DRAFT_MODEL")}))
		log.Print("status posts: drafted by Claude, checked by the claim checker")
	}
	d, err := desk.New(ix, st, set, opts...)
	if err != nil {
		return err
	}
	if err := d.Load(ctx, time.Now().UTC(), 30*time.Minute); err != nil {
		return fmt.Errorf("loading open incidents: %w", err)
	}
	secret := os.Getenv("DESK_WEBHOOK_SECRET")
	if secret == "" {
		log.Print("DESK_WEBHOOK_SECRET is not set: every result webhook will be refused")
	}
	srv := &http.Server{Addr: *addr, Handler: (&api.Server{Desk: d, Store: st, Index: ix, Secret: []byte(secret)}).Handler(),
		ReadHeaderTimeout: 5 * time.Second}
	ctx, stop := signal.NotifyContext(ctx, os.Interrupt)
	defer stop()
	go func() {
		t := time.NewTicker(15 * time.Second)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case now := <-t.C:
				if err := d.Tick(ctx, now.UTC()); err != nil {
					log.Printf("tick: %v", err)
				}
			}
		}
	}()
	go func() {
		<-ctx.Done()
		shut, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		srv.Shutdown(shut)
	}()
	log.Printf("desk listening on %s (%d checks, %d services)", *addr, len(cat.Checks), len(cat.Services))
	if err := srv.ListenAndServe(); err != http.ErrServerClosed {
		return err
	}
	return nil
}
