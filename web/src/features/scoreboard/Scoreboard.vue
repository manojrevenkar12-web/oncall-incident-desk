<script setup lang="ts">
import { computed } from "vue";
import type { Report } from "../../shared/report";
import { num, pct, range } from "../../shared/format";

const props = defineProps<{ report: Report }>();

const rows = computed(() => props.report.designs.filter((d) => !d.ablation));
const ablations = computed(() => props.report.designs.filter((d) => d.ablation));
const maxFalse = computed(() => Math.max(...props.report.designs.map((d) => d.stats.false_pages_per_week.value), 1));
const bar = (v: number) => `${Math.max(0.5, (100 * Math.log10(1 + v)) / Math.log10(1 + maxFalse.value))}%`;
</script>

<template>
  <section class="wrap block" aria-labelledby="score-title">
    <h2 id="score-title">Over {{ report.seeds.length }} simulated weeks</h2>
    <p class="intro">
      {{ report.truth.outages }} real outages, {{ report.truth.blips }} times a monitoring location lost its network,
      and every flaky check and duplicated webhook in between. An outage counts as caught when someone
      acknowledged it before it ended (plus five minutes). Small numbers are 95% intervals over seeds.
    </p>
    <div class="scroll">
      <table>
        <thead>
          <tr>
            <th scope="col">Design</th>
            <th scope="col">Outages caught</th>
            <th scope="col">Answered, median</th>
            <th scope="col" class="wide">False pages a week</th>
            <th scope="col">Pages per outage</th>
            <th scope="col">Announced on the status page</th>
            <th scope="col">False claims per 100 posts</th>
          </tr>
        </thead>
        <tbody>
          <tr v-for="d in [...rows, ...ablations]" :key="d.name" :class="{ ours: d.name === 'desk', ablation: d.ablation }">
            <th scope="row">
              <span class="label">{{ d.label }}</span>
              <span v-if="d.about && !d.ablation" class="about">{{ d.about }}</span>
            </th>
            <td>{{ pct(d.stats.recall.value) }}<small>{{ range(d.stats.recall, pct) }}</small></td>
            <td>{{ num(d.stats.mtta_median.value) }} min<small>first page at {{ num(d.stats.ttp_median.value, 0) }} min</small></td>
            <td class="wide">
              <span class="bar" :style="{ width: bar(d.stats.false_pages_per_week.value) }" aria-hidden="true"></span>
              {{ num(d.stats.false_pages_per_week.value) }}<small>{{ num(d.stats.night_false_pages_per_week.value) }} at night</small>
            </td>
            <td>{{ num(d.stats.pages_per_outage.value, 2) }}</td>
            <td>{{ pct(d.stats.notice_coverage.value) }}<small>after {{ num(d.stats.notice_median.value, 0) }} min</small></td>
            <td :class="{ warn: d.stats.bad_claims_per_100_posts.value > 1 }">{{ num(d.stats.bad_claims_per_100_posts.value) }}</td>
          </tr>
        </tbody>
      </table>
    </div>
    <p class="note">
      The bar for false pages uses a log scale, so a week of 184 pages and a week of 2 both stay readable. The
      rows below the desk take out one piece at a time to show what that piece is for.
    </p>
  </section>
</template>

<style scoped>
.block {
  padding-top: var(--space-7);
}
h2 {
  font-size: var(--step-2);
}
.intro {
  margin: var(--space-3) 0 var(--space-5);
  color: var(--ink-soft);
}
.scroll {
  overflow-x: auto;
}
table {
  width: 100%;
  border-collapse: collapse;
  font-size: var(--step--1);
}
th,
td {
  text-align: left;
  padding: var(--space-3) var(--space-3);
  border-bottom: 1px solid var(--rule);
  vertical-align: top;
}
thead th {
  color: var(--ink-muted);
  font-weight: 560;
}
td {
  font-size: var(--step-0);
  white-space: nowrap;
}
td small {
  display: block;
  font-size: 0.75rem;
  color: var(--ink-muted);
}
tbody th {
  font-weight: 520;
  min-width: 16rem;
}
.label {
  display: block;
  font-size: var(--step-0);
}
.about {
  display: block;
  color: var(--ink-muted);
  max-width: 40ch;
  margin-top: var(--space-1);
}
tr.ours th .label {
  font-weight: 680;
}
tr.ours {
  box-shadow: inset 3px 0 0 var(--page);
  background: var(--paper-raised);
}
tr.ablation th .label {
  color: var(--ink-soft);
}
tr.ablation:first-of-type th {
  border-top: 2px solid var(--rule);
}
.wide {
  min-width: 12rem;
}
.bar {
  display: block;
  height: 0.375rem;
  margin-bottom: var(--space-1);
  background: var(--page);
  border-radius: 2px;
}
.warn {
  color: var(--fail);
  font-weight: 620;
}
.note {
  margin-top: var(--space-3);
  color: var(--ink-muted);
  font-size: var(--step--1);
}
</style>
