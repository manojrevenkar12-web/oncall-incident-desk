<script setup lang="ts">
import type { Gate } from "../../shared/report";

defineProps<{ gates: Gate[]; world: string }>();
const show = (x: number) => (Math.abs(x) < 10 ? x.toFixed(3).replace(/\.?0+$/, "") : x.toFixed(1));
</script>

<template>
  <section class="wrap block" aria-labelledby="gates-title">
    <h2 id="gates-title">Release gates, {{ world === "heldout" ? "held-out world" : "dev world" }}</h2>
    <p class="intro" v-if="world === 'heldout'">
      Chosen on the dev world, then checked once here, on seeds and wording the desk never saw. These numbers
      are reported as they came out.
    </p>
    <p class="intro" v-else>Every rule and threshold was chosen on this world. The held-out world is the honest test.</p>
    <ul>
      <li v-for="g in gates" :key="g.name" :class="g.pass ? 'pass' : 'fail'">
        <span class="mark">{{ g.pass ? "Pass" : "Fail" }}</span>
        <span class="name">{{ g.name }}</span>
        <span class="num">{{ g.rule }} {{ show(g.want) }}, got {{ show(g.got) }}</span>
      </li>
    </ul>
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
ul {
  list-style: none;
  padding: 0;
  margin: 0;
  display: grid;
  gap: 1px;
  background: var(--rule);
  border: 1px solid var(--rule);
  border-radius: var(--radius-m);
  overflow: hidden;
}
li {
  display: grid;
  grid-template-columns: 4rem 1fr auto;
  gap: var(--space-4);
  padding: var(--space-3) var(--space-4);
  background: var(--paper-raised);
  align-items: baseline;
}
.mark {
  font-weight: 680;
}
.pass .mark {
  color: var(--pass);
}
.fail .mark {
  color: var(--fail);
}
.num {
  color: var(--ink-muted);
  font-size: var(--step--1);
}
@media (max-width: 40rem) {
  li {
    grid-template-columns: 3.5rem 1fr;
  }
  .num {
    grid-column: 2;
  }
}
</style>
