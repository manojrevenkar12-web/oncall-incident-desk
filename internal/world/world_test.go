package world

import (
	"reflect"
	"testing"
)

func load(t *testing.T) (Config, Phrases) {
	cfg, err := Load("../../config/world.json")
	if err != nil {
		t.Fatal(err)
	}
	ph, err := LoadPhrases("../../config/phrases.dev.json")
	if err != nil {
		t.Fatal(err)
	}
	return cfg, ph
}

func TestSameSeedSameWorld(t *testing.T) {
	cfg, ph := load(t)
	a, b := New(cfg, 5, ph), New(cfg, 5, ph)
	if !reflect.DeepEqual(a.Outages, b.Outages) || !reflect.DeepEqual(a.Blips, b.Blips) {
		t.Fatal("the same seed must draw the same outages and blips")
	}
	if c := New(cfg, 6, ph); reflect.DeepEqual(a.Outages, c.Outages) {
		t.Fatal("another seed should draw other outages")
	}
	if len(a.Outages) == 0 || len(a.Blips) == 0 {
		t.Fatal("a week should hold outages and blips")
	}
}

// Results are keyed by check, location and minute: asking in another order gives the same runs.
func TestResultsAreKeyedNotOrdered(t *testing.T) {
	cfg, ph := load(t)
	a, b := New(cfg, 9, ph), New(cfg, 9, ph)
	late := a.Results(5000)
	a.Results(10)
	for m := 0; m < 20; m++ {
		b.Results(m)
	}
	if !reflect.DeepEqual(late, b.Results(5000)) {
		t.Fatal("minute 5000 changed with the minutes asked before it")
	}
}

func TestFullOutageOfADependencyBreaksDependants(t *testing.T) {
	cfg, ph := load(t)
	for seed := uint64(1); seed < 40; seed++ {
		w := New(cfg, seed, ph)
		for _, o := range w.Outages {
			if o.Service == "auth" && o.Kind == "full" {
				has := map[string]bool{}
				for _, c := range o.Affected {
					has[c] = true
				}
				if !has["checkout-journey"] || !has["webhooks-delivery"] {
					t.Fatalf("auth down takes checkout and (through public-api) webhooks with it: %v", o.Affected)
				}
				return
			}
		}
	}
	t.Fatal("no full auth outage in 40 seeds")
}

func TestRespondIsKeyedByPersonAndMinute(t *testing.T) {
	cfg, ph := load(t)
	w := New(cfg, 3, ph)
	if w.Minutes() != cfg.Days*24*60 {
		t.Fatal("minutes")
	}
	inj := w.ModelDraft("inc-1", "investigating", []string{"Checkout"}, []string{"Search"}, nil, "checkout")
	if again := w.ModelDraft("inc-1", "investigating", []string{"Checkout"}, []string{"Search"}, nil, "checkout"); again != inj {
		t.Fatal("the stand-in model writes the same draft for the same incident")
	}
}
