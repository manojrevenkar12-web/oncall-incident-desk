# AGENTS.md

How to work in this repository. This applies to people and coding agents alike.

## Fast feedback

`make check` runs these steps, in about 20 seconds:

- checks formatting;
- runs `go vet`;
- runs every test with the race detector;
- checks that the README matches the reports.

To also run the store contract, the concurrent-duplicate test and the replay against Postgres, set
`DESK_TEST_DATABASE_URL=postgres://…`. CI does this on a `postgres:16` service.

Other commands:

- `make study` runs the dev study, about a minute on two cores.
- `cd web && npm test` runs the console tests in about 3 seconds.

## Layout

| Path | What it is | May import |
|---|---|---|
| `internal/oncall` | the shared vocabulary | standard library |
| `internal/store` | Postgres and memory behind one contract | oncall |
| `internal/drafts` | template, Claude drafter, claim checker | oncall |
| `internal/desk` | the engine | oncall, store, drafts |
| `internal/api` | HTTP | product |
| `internal/world` | the simulated fleet and its truth | oncall |
| `internal/lab` | plays the world through each design, scores against the truth | everything |
| `cmd/desk` | serve, study, readme, replay, catalog | everything |
| `web/` | the Vue console, reading `reports/*.json` | |

"Product" means oncall, store, drafts, desk and api.

## Invariants

1. **The truth stays in the world.** No product package imports `world` or `lab`; `TestInvariants` checks the
   imports. Only `lab` uses these, and only to score designs after the fact:
   - which outages really happened;
   - which checks they broke;
   - what errors the stand-in model put into a draft.
2. **The world does not read the desk's settings.** `world/` reads only `config/world.json` or
   `config/heldout.json` and its phrase bank, and imports only `oncall`. It never opens these files
   (`TestInvariants` checks):
   - `config/desk.json`
   - `config/designs.json`
   - `config/gates.json`
3. **Same luck for every design.** Every draw is keyed by the seed and by what it is about, never by call
   order:
   - a run by (check, location, minute);
   - an answer by (person, minute);
   - a draft by (incident, kind).

   So every design meets the same outages, and the same page gets the same answer. Tests:
   `TestResultsAreKeyedNotOrdered`, `TestSameLuckForEveryDesign`.
4. **The model reads and code decides.** The model writes the words of a status post. The checker decides
   whether they are published. If they are not, the template built from the facts goes out instead. In the
   `model_only` design the model also decides when to page. That design is the baseline the desk is compared
   with; it is not a mode the product offers.
5. **A result is kept once.** A delivery seen before changes nothing (a Postgres primary key; a map in
   memory). A result older than the newest one for its check and location is stored, but it does not change
   the check's state. Tests: `TestContract`, `TestConcurrentDuplicatesKeptOnce`, `TestDuplicatesAndLateResults`.
6. **The store never changes a decision.** `desk replay` plays six hours into Postgres and into memory and
   requires identical incidents, pages and posts. CI runs it.
7. **Settings in config.** These live in `config/desk.json`:
   - confirmation and recovery rules;
   - the location filter and the noise floor;
   - grouping, roll-up, the reopen grace and the escalation policy;
   - publishing and the checker.

   Each design is a set of overrides in `config/designs.json`; the gates are in `config/gates.json`.
8. **Generated reports.** `reports/*.json` come from `desk study`. The README results block comes from
   `desk readme`. The console reads the reports. Never edit any of them by hand; `make check` and CI fail when
   they are stale.

## The held-out rule

Every choice was made on the dev world (seeds 1–20):

- the rules and thresholds;
- the escalation policy;
- the checker's categories;
- the gates.

The held-out world (`config/heldout.json`, seeds 9101–9120) differs from the dev world:

- four locations instead of three;
- 20% more outages, with more degraded ones;
- more blips, flakes and duplicates;
- slower nights;
- status-post errors worded in ways the dev bank never used.

`make heldout` runs it once and writes `reports/heldout.lock`. After that the command refuses to run, and CI
checks that it refuses. Its numbers are reported as they came out, including any gate that failed. Do not tune
anything against the held-out report. If something changes after it ran, report the change as a change made
after seeing it.

## Conventions

- Go 1.24, standard library first; one dependency (`lib/pq`).
- Times are UTC. The night (22:00–07:00) is computed in Berlin winter time.
- Errors a person can see say what happened and what to do next.
- Commits are authored by Manoj Kumar Prakash.
