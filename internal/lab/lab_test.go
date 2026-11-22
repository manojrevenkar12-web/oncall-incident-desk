package lab

import (
	"context"
	"errors"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/manojrevenkar12-web/oncall-incident-desk/internal/world"
)

const mod = "github.com/manojrevenkar12-web/oncall-incident-desk/internal/"

func imports(t *testing.T, pkg string) []string {
	var out []string
	files, _ := filepath.Glob(filepath.Join("..", pkg, "*.go"))
	for _, f := range files {
		if strings.HasSuffix(f, "_test.go") {
			continue
		}
		af, err := parser.ParseFile(token.NewFileSet(), f, nil, parser.ImportsOnly)
		if err != nil {
			t.Fatal(err)
		}
		for _, im := range af.Imports {
			out = append(out, strings.Trim(im.Path.Value, `"`))
		}
	}
	return out
}

// The truth stays in the world, and the world does not read the desk's settings.
func TestInvariants(t *testing.T) {
	for _, pkg := range []string{"oncall", "store", "desk", "drafts", "api"} {
		for _, im := range imports(t, pkg) {
			if im == mod+"world" || im == mod+"lab" {
				t.Errorf("%s imports %s: no product package may see the truth", pkg, im)
			}
		}
	}
	for _, im := range imports(t, "world") {
		if strings.HasPrefix(im, mod) && im != mod+"oncall" {
			t.Errorf("world imports %s: it may import only the vocabulary", im)
		}
	}
	files, _ := filepath.Glob("../world/*.go")
	for _, f := range files {
		b, _ := os.ReadFile(f)
		if strings.Contains(string(b), "desk.json") || strings.Contains(string(b), "designs.json") || strings.Contains(string(b), "gates.json") {
			t.Errorf("%s mentions the desk's settings", f)
		}
	}
}

func setup(t *testing.T) ([]Design, world.Config, world.Phrases) {
	ds, _, err := LoadDesigns("../..")
	if err != nil {
		t.Fatal(err)
	}
	cfg, err := world.Load("../../config/world.json")
	if err != nil {
		t.Fatal(err)
	}
	cfg.Days = 2
	ph, err := world.LoadPhrases("../../config/phrases.dev.json")
	if err != nil {
		t.Fatal(err)
	}
	return ds, cfg, ph
}

func byName(ds []Design, n string) Design {
	for _, d := range ds {
		if d.Name == n {
			return d
		}
	}
	return Design{}
}

func TestPlayIsDeterministic(t *testing.T) {
	ds, cfg, ph := setup(t)
	a, err := Play(context.Background(), byName(ds, "desk"), cfg, ph, 4)
	if err != nil {
		t.Fatal(err)
	}
	b, _ := Play(context.Background(), byName(ds, "desk"), cfg, ph, 4)
	if !reflect.DeepEqual(a.Pages, b.Pages) || !reflect.DeepEqual(a.Posts, b.Posts) || !reflect.DeepEqual(a.Incidents, b.Incidents) {
		t.Fatal("the same design on the same seed must do the same things")
	}
}

// Every design meets the same outages: the world does not depend on what the design does.
func TestSameLuckForEveryDesign(t *testing.T) {
	ds, cfg, ph := setup(t)
	var first []world.Outage
	for _, d := range ds[:3] {
		r, err := Play(context.Background(), d, cfg, ph, 2)
		if err != nil {
			t.Fatal(err)
		}
		if first == nil {
			first = r.World.Outages
		} else if !reflect.DeepEqual(first, r.World.Outages) {
			t.Fatalf("%s met other outages", d.Name)
		}
		s := Measure(r)
		if s.Outages != len(first) {
			t.Fatal("scored outages differ")
		}
	}
}

func TestDeskBeatsTheStatusQuoOnNoise(t *testing.T) {
	ds, cfg, ph := setup(t)
	sq, _ := Play(context.Background(), byName(ds, "status_quo"), cfg, ph, 7)
	dk, _ := Play(context.Background(), byName(ds, "desk"), cfg, ph, 7)
	a, b := Measure(sq), Measure(dk)
	if b.FalsePages*5 > a.FalsePages || b.Detected < a.Detected {
		t.Fatalf("status quo %d false pages, %d detected; desk %d, %d", a.FalsePages, a.Detected, b.FalsePages, b.Detected)
	}
}

func TestHeldoutRunsOnce(t *testing.T) {
	root := t.TempDir()
	os.MkdirAll(filepath.Join(root, "reports"), 0o755)
	os.WriteFile(filepath.Join(root, "reports/heldout.lock"), []byte("ran"), 0o644)
	if _, err := Study(context.Background(), root, "heldout"); !errors.Is(err, ErrLocked) {
		t.Fatalf("want ErrLocked, got %v", err)
	}
}

func TestBootstrapIsStable(t *testing.T) {
	ss := []Score{{Detected: 9, Outages: 10}, {Detected: 10, Outages: 10}, {Detected: 7, Outages: 10}}
	a := bootstrap(ss, metrics["recall"], 500, "k")
	b := bootstrap(ss, metrics["recall"], 500, "k")
	if a != b || a.Lo > a.Value || a.Hi < a.Value {
		t.Fatalf("%+v %+v", a, b)
	}
}
