// Copies the generated lab reports into public/ so the console serves them. Missing reports are skipped:
// the console shows an empty state for a world that has not run.
import { copyFileSync, existsSync, mkdirSync } from "node:fs";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";

const here = dirname(fileURLToPath(import.meta.url));
const out = join(here, "..", "public", "reports");
mkdirSync(out, { recursive: true });
for (const name of ["dev", "heldout"]) {
  const src = join(here, "..", "..", "reports", `${name}.json`);
  if (existsSync(src)) {
    copyFileSync(src, join(out, `${name}.json`));
    console.log(`reports/${name}.json copied`);
  } else {
    console.log(`reports/${name}.json missing: the console will say it has not run`);
  }
}
