// Command desk serves the on-call desk and runs its lab.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"

	"github.com/manojrevenkar12-web/oncall-incident-desk/internal/lab"
)

func main() {
	if len(os.Args) < 2 {
		usage()
	}
	ctx := context.Background()
	var err error
	switch os.Args[1] {
	case "study":
		fs := flag.NewFlagSet("study", flag.ExitOnError)
		which := fs.String("world", "dev", "dev, or heldout (runs once)")
		fs.Parse(os.Args[2:])
		var rep *lab.Report
		rep, err = lab.Study(ctx, ".", *which)
		if err == nil {
			printSummary(rep)
		}
	case "readme":
		err = readme(".")
	case "serve":
		err = serve(ctx, os.Args[2:])
	case "replay":
		err = replay(ctx, os.Args[2:])
	case "catalog":
		err = catalog(os.Args[2:])
	default:
		usage()
	}
	if errors.Is(err, lab.ErrLocked) {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(3)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "desk:", err)
		os.Exit(1)
	}
}

func usage() {
	fmt.Fprintln(os.Stderr, `usage: desk <command>
  study  -world dev|heldout   play every design over every seed and write reports/<world>.json
  readme                      write the README results block from the reports
  serve  -addr :8080          run the desk (memory, or Postgres with -dsn / DATABASE_URL)
  replay -dsn <postgres>      play one dev day into Postgres and into memory and compare
  catalog                     print the dev world's catalogue (config/catalog.example.json)`)
	os.Exit(2)
}

func printSummary(r *lab.Report) {
	fmt.Printf("%s world: %d seeds x %d days, %d checks from %d locations (%s)\n", r.World, len(r.Seeds), r.Days, r.Checks, r.Locations, r.Took)
	fmt.Printf("%-22s %7s %10s %6s %8s %8s %7s %7s %7s %6s\n", "design", "recall", "ttp/mtta", "p90", "false/wk", "night/wk", "pg/out", "posted", "notice", "bad%")
	for _, d := range r.Designs {
		s := d.Stats
		fmt.Printf("%-22s %7.3f %4.1f/%-5.1f %6.1f %8.1f %8.1f %7.2f %7.2f %7.1f %6.2f\n", d.Name, s["recall"].Value, s["ttp_median"].Value, s["mtta_median"].Value,
			s["mtta_p90"].Value, s["false_pages_per_week"].Value, s["night_false_pages_per_week"].Value, s["pages_per_outage"].Value,
			s["notice_coverage"].Value, s["notice_median"].Value, s["bad_claims_per_100_posts"].Value)
	}
	for _, g := range r.Gates {
		mark := "PASS"
		if !g.Pass {
			mark = "FAIL"
		}
		fmt.Printf("  %s  %s: %s %.3f, got %.3f\n", mark, g.Name, g.Rule, g.Want, g.Got)
	}
	c := r.Checker
	fmt.Printf("checker: %d drafts, %d with an error, caught %d (%.3f), clean refused %d (%.3f)\n", c.Drafts, c.WithError, c.Caught, c.Recall, c.CleanRejected, c.FalseRejectRate)
}
