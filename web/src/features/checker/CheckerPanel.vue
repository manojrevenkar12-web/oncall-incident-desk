<script setup lang="ts">
import { computed } from "vue";
import type { Report } from "../../shared/report";
import { ERROR_NAMES, pct } from "../../shared/format";

const props = defineProps<{ report: Report }>();
const c = computed(() => props.report.checker);

// Marks the words the checker objected to, so the reason is visible in the sentence itself.
function marked(text: string, violations: string[] | null) {
  const spans = (violations ?? [])
    .map((v) => v.split(": ").slice(1).join(": "))
    .filter((w) => w && !w.startsWith("a number") && text.toLowerCase().includes(w.toLowerCase()));
  const parts: { t: string; hit: boolean }[] = [];
  let rest = text;
  while (rest) {
    let best = -1;
    let word = "";
    for (const w of spans) {
      const i = rest.toLowerCase().indexOf(w.toLowerCase());
      if (i >= 0 && (best < 0 || i < best)) {
        best = i;
        word = rest.slice(i, i + w.length);
      }
    }
    if (best < 0) {
      parts.push({ t: rest, hit: false });
      break;
    }
    if (best > 0) parts.push({ t: rest.slice(0, best), hit: false });
    parts.push({ t: word, hit: true });
    rest = rest.slice(best + word.length);
  }
  return parts;
}
</script>

<template>
  <section class="wrap block" aria-labelledby="checker-title">
    <h2 id="checker-title">What the status page was not allowed to say</h2>
    <p class="intro">
      A model writes each status post; code decides whether it goes out. An investigating post may name only
      components whose checks are failing, may not promise a recovery time, may not guess a cause and may not
      mention anything internal. A draft that breaks a rule is replaced by a template built from the facts.
    </p>

    <div class="numbers">
      <p><strong>{{ c.drafts }}</strong> drafts written</p>
      <p><strong>{{ c.with_error }}</strong> carried an error the stand-in model put there</p>
      <p><strong>{{ pct(c.recall) }}</strong> of those refused</p>
      <p><strong>{{ c.clean_rejected }}</strong> clean drafts refused by mistake</p>
    </div>

    <table v-if="c.by_kind.length" class="kinds">
      <thead>
        <tr><th scope="col">Error in the draft</th><th scope="col">Drafts</th><th scope="col">Refused</th></tr>
      </thead>
      <tbody>
        <tr v-for="k in c.by_kind" :key="k.kind" :class="{ leak: k.caught < k.injected }">
          <th scope="row">{{ ERROR_NAMES[k.kind] ?? k.kind }}</th>
          <td>{{ k.injected }}</td>
          <td>{{ k.caught }}</td>
        </tr>
      </tbody>
    </table>

    <div class="cols">
      <div>
        <h3>Refused</h3>
        <ul v-if="c.refused?.length" class="drafts">
          <li v-for="(e, i) in c.refused.slice(0, 6)" :key="i">
            <p>
              <template v-for="(p, j) in marked(e.text, e.violations)" :key="j"><mark v-if="p.hit">{{ p.t }}</mark><template v-else>{{ p.t }}</template></template>
            </p>
            <span class="why">{{ (e.violations ?? []).map((v) => ERROR_NAMES[v.split(':')[0]] ?? v).join(" · ") }}</span>
          </li>
        </ul>
        <p v-else class="empty">No draft was refused in this world.</p>
      </div>
      <div>
        <h3>Got through with an error</h3>
        <ul v-if="c.through?.length" class="drafts through">
          <li v-for="(e, i) in c.through.slice(0, 6)" :key="i">
            <p>{{ e.text }}</p>
            <span class="why">{{ (e.injected ?? []).map((k) => ERROR_NAMES[k] ?? k).join(" · ") }}</span>
          </li>
        </ul>
        <p v-else class="empty">Nothing with an error reached the status page in this world.</p>
      </div>
    </div>
  </section>
</template>

<style scoped>
.block {
  padding-top: var(--space-7);
}
h2 {
  font-size: var(--step-2);
}
h3 {
  font-size: var(--step-1);
  margin-bottom: var(--space-3);
}
.intro {
  margin: var(--space-3) 0 var(--space-5);
  color: var(--ink-soft);
}
.numbers {
  display: flex;
  flex-wrap: wrap;
  gap: var(--space-5) var(--space-6);
  margin-bottom: var(--space-5);
}
.numbers p {
  color: var(--ink-soft);
}
.numbers strong {
  display: block;
  font-size: var(--step-2);
  color: var(--ink);
  font-weight: 640;
}
.kinds {
  border-collapse: collapse;
  margin-bottom: var(--space-6);
  font-size: var(--step--1);
}
.kinds th,
.kinds td {
  padding: var(--space-2) var(--space-5) var(--space-2) 0;
  border-bottom: 1px solid var(--rule);
  text-align: left;
}
.kinds thead th {
  color: var(--ink-muted);
  font-weight: 560;
}
.kinds tbody th {
  font-weight: 500;
}
.kinds tr.leak td:last-child {
  color: var(--fail);
  font-weight: 640;
}
.cols {
  display: grid;
  grid-template-columns: 1fr 1fr;
  gap: var(--space-6);
}
.drafts {
  list-style: none;
  padding: 0;
  margin: 0;
  display: grid;
  gap: var(--space-4);
}
.drafts li {
  padding-left: var(--space-4);
  border-left: 3px solid var(--refused);
}
.drafts.through li {
  border-left-color: var(--fault);
}
mark {
  background: color-mix(in srgb, var(--refused) 30%, transparent);
  color: inherit;
  padding: 0 0.1em;
  border-radius: 2px;
}
.why {
  display: block;
  margin-top: var(--space-1);
  font-size: var(--step--1);
  color: var(--ink-muted);
}
.empty {
  color: var(--ink-muted);
}
@media (max-width: 50rem) {
  .cols {
    grid-template-columns: 1fr;
  }
}
</style>
