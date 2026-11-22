<script setup lang="ts">
import { computed, ref } from "vue";
import { motion, AnimatePresence } from "motion-v";
import type { DesignReport, Showcase } from "../../shared/report";
import { MAIN_DESIGNS } from "../../shared/report";
import { KIND_NAMES, minutesBetween, when } from "../../shared/format";
import { failingShare, groups, locations, span, summary, type Group } from "./replay";

const props = defineProps<{ showcase: Showcase; designs: DesignReport[] }>();

const withAblations = ref(false);
const selected = ref<{ lane: string; group: Group } | null>(null);

const sc = computed(() => props.showcase);
const s = computed(() => span(sc.value));
const locs = computed(() => locations(sc.value));
const minutes = computed(() => {
  const out: number[] = [];
  for (let m = s.value.min; m <= s.value.max; m++) out.push(m);
  return out;
});
const duration = computed(() => minutesBetween(sc.value.outage.start, sc.value.outage.end));
const x = (m: number) => `${((m - s.value.min) / (s.value.max - s.value.min + 1)) * 100}%`;
const width = (a: number, b: number) => `${((b - a) / (s.value.max - s.value.min + 1)) * 100}%`;
const tickStep = computed(() => {
  const n = s.value.max - s.value.min;
  return n <= 50 ? 5 : n <= 100 ? 10 : n <= 180 ? 15 : 30;
});
const ticks = computed(() => minutes.value.filter((m) => m % tickStep.value === 0));

const lanes = computed(() =>
  sc.value.lanes
    .filter((l) => withAblations.value || MAIN_DESIGNS.includes(l.design))
    .map((l) => ({ ...l, groups: groups(l), summary: summary(l) })),
);

const headline = computed(() => {
  const o = sc.value.outage;
  const pub = o.public?.length ?? 0;
  const verb = KIND_NAMES[o.kind] ?? "broke";
  const tail = pub > 1 ? ` and took ${pub} public components with it` : "";
  return `${when(o.start)}: ${o.service} ${verb} for ${duration.value} minutes${tail}.`;
});

const label = (g: Group) => {
  const n = g.events.length;
  const what = { page: "page", ack: "acknowledgement", post: "status post", refused: "refused draft" }[g.kind];
  return `${n} ${what}${n > 1 ? "s" : ""} at ${g.minute >= 0 ? "+" : ""}${g.minute} min${g.about ? "" : ", not about this outage"}`;
};

const plus = (m: number | null) => (m === null ? "none" : `+${Math.round(m)} min`);
const pick = (lane: string, group: Group) => {
  selected.value = selected.value?.group.key === group.key && selected.value.lane === lane ? null : { lane, group };
};
const designLabel = (name: string) => props.designs.find((d) => d.name === name)?.label ?? name;
</script>

<template>
  <section class="night" aria-labelledby="replay-title">
    <div class="wrap">
      <h1 id="replay-title" class="headline">{{ headline }}</h1>
      <p class="lede">
        The same outage, the same people and the same luck, played through each way of running on-call. Every
        mark is something that happened: a page, an acknowledgement, a status post, or a draft the desk refused.
      </p>

      <div class="legend" aria-hidden="true">
        <span><i class="mk page"></i>Page</span>
        <span><i class="mk page hollow"></i>Page about nothing real</span>
        <span><i class="mk ack"></i>Acknowledged</span>
        <span><i class="mk post"></i>Status post</span>
        <span><i class="mk post bad"></i>Post with a false claim</span>
        <span><i class="mk refused"></i>Draft refused by the checker</span>
      </div>

      <div class="chart" role="group" aria-label="Timeline of the outage">
        <div class="row strip-head">
          <span class="name">Failing runs</span>
          <div class="track axis">
            <span v-for="t in ticks" :key="t" class="tick" :style="{ left: x(t) }">{{ t === 0 ? "start" : `${t > 0 ? "+" : ""}${t}` }}</span>
          </div>
        </div>
        <div v-for="l in locs" :key="l" class="row strip">
          <span class="name loc">{{ l }}</span>
          <div class="track">
            <span
              v-for="m in minutes"
              :key="m"
              class="cell"
              :style="{ left: x(m), width: width(m, m + 1), opacity: 0.12 + 0.88 * failingShare(sc, l, m, locs.length) }"
            ></span>
          </div>
        </div>

        <div class="lanes">
          <div v-for="lane in lanes" :key="lane.design" class="row lane" :class="{ ours: lane.design === 'desk' }">
            <span class="name">{{ designLabel(lane.design) }}</span>
            <div class="track">
              <span class="band" :style="{ left: x(0), width: width(0, duration) }" aria-hidden="true"></span>
              <motion.button
                v-for="(g, i) in lane.groups"
                :key="g.key"
                type="button"
                class="mk"
                :class="[g.kind, { hollow: g.kind === 'page' && !g.about, bad: g.events.some((e) => e.bad), on: selected?.group.key === g.key && selected?.lane === lane.design }]"
                :style="{ left: x(g.minute), top: `${(['page', 'ack', 'refused', 'post'].indexOf(g.kind)) * 22 + 4}%` }"
                :aria-label="label(g)"
                :initial="{ opacity: 0, scale: 0.4 }"
                :animate="{ opacity: 1, scale: 1 }"
                :transition="{ delay: 0.2 + ((g.minute - s.min) / (s.max - s.min + 1)) * 1.1 + i * 0.003, duration: 0.25 }"
                @click="pick(lane.design, g)"
              >
                <span v-if="g.events.length > 1" class="count">{{ g.events.length }}</span>
              </motion.button>
            </div>
            <dl class="sum">
              <div><dt>Pages</dt><dd>{{ lane.summary.pages }} to {{ lane.summary.people }}</dd></div>
              <div><dt>Answered</dt><dd>{{ plus(lane.summary.firstAck) }}</dd></div>
              <div><dt>First post</dt><dd :class="{ warn: lane.summary.badPosts > 0 }">{{ plus(lane.summary.firstPost) }}<template v-if="lane.summary.badPosts"> · {{ lane.summary.badPosts }} false</template></dd></div>
            </dl>
          </div>
        </div>
      </div>

      <AnimatePresence>
        <motion.div
          v-if="selected"
          :key="selected.lane + selected.group.key"
          class="detail"
          :initial="{ opacity: 0, y: -6 }"
          :animate="{ opacity: 1, y: 0 }"
          :exit="{ opacity: 0 }"
          :transition="{ duration: 0.18 }"
        >
          <p class="detail-head">{{ designLabel(selected.lane) }} · {{ label(selected.group) }}</p>
          <ul>
            <li v-for="(e, i) in selected.group.events" :key="i">
              <template v-if="e.kind === 'page'">{{ e.who }} paged as {{ e.level }}{{ e.about ? "" : " — nothing was wrong" }}</template>
              <template v-else-if="e.kind === 'ack'">{{ e.who }} acknowledged</template>
              <template v-else-if="e.kind === 'refused'">
                <q>{{ e.text }}</q>
                <span class="why">Refused: {{ e.why }}. The safe template went out instead.</span>
              </template>
              <template v-else>
                <q :class="{ bad: e.bad }">{{ e.text }}</q>
                <span class="why">{{ e.level }} post by {{ e.who }}<template v-if="e.bad"> — carries a claim nobody could back</template><template v-if="e.fallback"> — the template, after a refused draft</template></span>
              </template>
            </li>
          </ul>
        </motion.div>
      </AnimatePresence>

      <label class="toggle">
        <input v-model="withAblations" type="checkbox" />
        Show the desk with one piece taken out at a time
      </label>
    </div>
  </section>
</template>

<style scoped>
.night {
  background: var(--night);
  color: var(--night-ink);
  padding: var(--space-7) 0 var(--space-6);
}
.headline {
  font-size: var(--step-3);
  font-weight: 640;
  max-width: 24ch;
  letter-spacing: -0.02em;
}
.lede {
  margin-top: var(--space-4);
  color: var(--night-muted);
  font-size: var(--step-1);
  max-width: 60ch;
}
.legend {
  display: flex;
  flex-wrap: wrap;
  gap: var(--space-2) var(--space-5);
  margin: var(--space-6) 0 var(--space-4);
  font-size: var(--step--1);
  color: var(--night-muted);
}
.legend span {
  display: inline-flex;
  align-items: center;
  gap: var(--space-2);
}
.legend .mk {
  position: static;
  transform: none;
}
.chart {
  display: grid;
  gap: 2px;
}
.row {
  display: grid;
  grid-template-columns: 13rem 1fr 15rem;
  align-items: center;
  gap: var(--space-4);
}
.name {
  font-size: var(--step--1);
  color: var(--night-muted);
}
.loc {
  font-variant-numeric: tabular-nums;
}
.track {
  position: relative;
  height: 100%;
}
.axis {
  height: 1.5rem;
}
.tick {
  position: absolute;
  bottom: 0;
  font-size: 0.75rem;
  color: var(--night-muted);
  transform: translateX(-50%);
}
.strip .track {
  height: 0.875rem;
}
.cell {
  position: absolute;
  top: 0;
  bottom: 0;
  background: var(--fault);
  border-right: 1px solid var(--night);
}
.lanes {
  position: relative;
  margin-top: var(--space-4);
  display: grid;
  gap: 2px;
}
.band {
  position: absolute;
  top: -0.5rem;
  bottom: -0.5rem;
  background: color-mix(in srgb, var(--fault) 10%, transparent);
  border-left: 1px solid color-mix(in srgb, var(--fault) 55%, transparent);
  pointer-events: none;
}
.lane {
  min-height: 4.25rem;
  border-top: 1px solid var(--night-rule);
  padding: var(--space-2) 0;
}
.lane .name {
  color: var(--night-ink);
  font-size: var(--step-0);
}
.lane.ours .name {
  font-weight: 640;
}
.lane.ours {
  background: color-mix(in srgb, var(--night-raised) 70%, transparent);
}
.lane .track {
  height: 3.5rem;
}
.mk {
  position: absolute;
  width: 0.8rem;
  height: 0.8rem;
  padding: 0;
  border: 0;
  transform: translateX(-50%);
  cursor: pointer;
  display: inline-block;
}
.mk.page {
  border-radius: 50%;
  background: var(--page);
}
.mk.page.hollow {
  background: transparent;
  box-shadow: inset 0 0 0 2px var(--page);
}
.mk.ack {
  border-radius: 50%;
  background: transparent;
  box-shadow: inset 0 0 0 3px var(--ack);
}
.mk.post {
  border-radius: var(--radius-s);
  background: var(--post);
}
.mk.post.bad {
  background: var(--fault);
  box-shadow: 0 0 0 2px var(--night), 0 0 0 3px var(--fault);
}
.mk.refused {
  border-radius: var(--radius-s);
  background: repeating-linear-gradient(135deg, var(--refused) 0 2px, transparent 2px 4px);
  box-shadow: inset 0 0 0 1px var(--refused);
}
.mk.on {
  outline: 2px solid var(--night-ink);
  outline-offset: 2px;
}
.count {
  position: absolute;
  left: 100%;
  top: -0.35rem;
  margin-left: 2px;
  font-size: 0.6875rem;
  font-weight: 640;
  color: var(--night-ink);
  white-space: nowrap;
}
.sum {
  display: grid;
  grid-template-columns: repeat(3, 1fr);
  gap: var(--space-2);
  margin: 0;
  font-size: var(--step--1);
}
.sum dt {
  color: var(--night-muted);
}
.sum dd {
  margin: 0;
}
.sum .warn {
  color: #ff9b8f;
}
.detail {
  margin-top: var(--space-5);
  padding: var(--space-4) var(--space-5);
  background: var(--night-raised);
  border-radius: var(--radius-m);
  max-width: 52rem;
}
.detail-head {
  font-weight: 620;
  margin-bottom: var(--space-2);
}
.detail ul {
  margin: 0;
  padding-left: var(--space-4);
  display: grid;
  gap: var(--space-2);
}
.detail q {
  display: block;
  font-style: normal;
}
.detail q.bad {
  text-decoration: underline wavy var(--fault);
  text-underline-offset: 0.3em;
}
.why {
  display: block;
  color: var(--night-muted);
  font-size: var(--step--1);
}
.toggle {
  display: inline-flex;
  gap: var(--space-2);
  align-items: center;
  margin-top: var(--space-5);
  color: var(--night-muted);
  font-size: var(--step--1);
  cursor: pointer;
}
@media (max-width: 60rem) {
  .row {
    grid-template-columns: 7rem 1fr;
  }
  .sum {
    grid-column: 1 / -1;
  }
  .headline {
    font-size: var(--step-2);
  }
}
</style>
