package main

import (
	"encoding/json"
	"os"

	"github.com/manojrevenkar12-web/oncall-incident-desk/internal/world"
)

func catalog([]string) error {
	cfg, err := world.Load("config/world.json")
	if err != nil {
		return err
	}
	w := world.New(cfg, 1, world.Phrases{})
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	return enc.Encode(w.Catalog())
}
