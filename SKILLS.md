# SKILLS.md

Repeatable tasks, each with its own steps and the check that says it worked.

## Change a rule of the desk

1. Change the setting in `config/desk.json`. If the rule is new, add it to `desk.Settings`, to
   `Settings.Validate` and to `config/desk.json`.
2. Give each baseline in `config/designs.json` the value that keeps it what it claims to be.
3. Add a test to `internal/desk/desk_test.go` that fails without the rule.
4. Run `make study && make readme`, read the dev table, and commit the code, the reports and the README together.
5. Done when `make check` is green. Do not look at `reports/heldout.json` to decide.

## Add a design or an ablation

1. Add an entry to `config/designs.json` with overrides, `drafter` and, for an ablation, `"ablation": true`.
2. Run `make study && make readme`.
3. Done when the README shows the new row and CI's reproduction step passes.

## Add an outage kind or a kind of noise to the world

1. Add it to `world.Config` and `config/world.json`, and draw it in `internal/world` with keyed draws only
   (`w.u(...)` or `mix(base, minute)`).
2. Add a test to `world_test.go` showing it appears and is keyed.
3. Never read `config/desk.json` from the world.

## Add a claim the checker must refuse

1. Add a category, not a phrase, to `internal/drafts/drafts.go`.
2. Add a case to `TestCheckerCatchesEachCategory`, and make sure the template still passes:
   `TestTemplatePassesItsOwnChecker`.
3. If the stand-in model should make the new mistake, add phrasing to `config/phrases.dev.json`, never to the
   held-out bank.

## Connect a real drafter

1. Implement `drafts.Drafter`; `drafts.Claude` is the example.
2. Pass it with `desk.WithDrafter`.
3. The checker and the template stay in front of it; nothing else changes.

## Run the held-out study

Run it once, when every dev choice is final:

1. `make heldout` writes `reports/heldout.json` and `reports/heldout.lock`.
2. `make readme`, then `cd web && npm run build` and refresh the screenshots.
3. Commit everything. Report failed gates as they are.

## Refresh the console screenshots

1. Run `cd web && npm run build`, then serve `web/dist` on port 4173.
2. Run the Playwright script in `docs/screenshots/README.md` with system Chrome.
3. Look at every image before committing it.
