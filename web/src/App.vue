<script setup lang="ts">
import { ref } from "vue";
import { MotionConfig } from "motion-v";
import { useReport, type World } from "./shared/report";
import StateBlock from "./shared/StateBlock.vue";
import ReplayTimeline from "./features/replay/ReplayTimeline.vue";
import Scoreboard from "./features/scoreboard/Scoreboard.vue";
import CheckerPanel from "./features/checker/CheckerPanel.vue";
import GateList from "./features/gates/GateList.vue";
import Limits from "./features/limits/Limits.vue";

const world = ref<World>("dev");
const state = useReport(world);
</script>

<template>
  <MotionConfig reduced-motion="user">
    <header class="top">
      <div class="wrap bar">
        <p class="brand">On-call incident desk</p>
        <div class="switch" role="radiogroup" aria-label="Which world">
          <button type="button" role="radio" :aria-checked="world === 'dev'" @click="world = 'dev'">Dev world</button>
          <button type="button" role="radio" :aria-checked="world === 'heldout'" @click="world = 'heldout'">Held-out world</button>
        </div>
      </div>
    </header>
    <main>
      <div v-if="state.status !== 'ready'" class="wrap pad">
        <StateBlock :kind="state.status" :message="state.status === 'error' ? state.message : undefined" :world="world" />
      </div>
      <template v-else>
        <ReplayTimeline :showcase="state.report.showcase" :designs="state.report.designs" />
        <Scoreboard :report="state.report" />
        <CheckerPanel :report="state.report" />
        <GateList :gates="state.report.gates" :world="world" />
      </template>
      <Limits />
    </main>
  </MotionConfig>
</template>

<style scoped>
.top {
  background: var(--night);
  color: var(--night-ink);
  border-bottom: 1px solid var(--night-rule);
}
.bar {
  display: flex;
  justify-content: space-between;
  align-items: center;
  gap: var(--space-4);
  padding-top: var(--space-3);
  padding-bottom: var(--space-3);
}
.brand {
  font-weight: 640;
}
.switch {
  display: inline-flex;
  border: 1px solid var(--night-rule);
  border-radius: var(--radius-m);
  overflow: hidden;
}
.switch button {
  background: transparent;
  border: 0;
  padding: var(--space-2) var(--space-4);
  color: var(--night-muted);
  cursor: pointer;
  font-size: var(--step--1);
}
.switch button[aria-checked="true"] {
  background: var(--night-raised);
  color: var(--night-ink);
  box-shadow: inset 0 -2px 0 var(--page);
}
.pad {
  padding-top: var(--space-6);
}
</style>
