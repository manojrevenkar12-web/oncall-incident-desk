<script setup lang="ts">
defineProps<{ kind: "loading" | "missing" | "error"; message?: string; world?: string }>();
</script>

<template>
  <div class="state" :class="kind" role="status" aria-live="polite">
    <template v-if="kind === 'loading'">
      <span class="pulse" aria-hidden="true"></span>
      <p>Reading the {{ world }} report…</p>
    </template>
    <template v-else-if="kind === 'missing'">
      <p class="title">The {{ world }} study has not run yet.</p>
      <p v-if="world === 'heldout'">It runs once, at the end: <code>make heldout</code>, then <code>npm run build</code>.</p>
      <p v-else>Run <code>make study</code>, then <code>npm run build</code>.</p>
    </template>
    <template v-else>
      <p class="title">The report could not be shown.</p>
      <p>{{ message }}</p>
    </template>
  </div>
</template>

<style scoped>
.state {
  display: grid;
  gap: var(--space-2);
  padding: var(--space-6) var(--space-5);
  border: 1px dashed var(--rule);
  border-radius: var(--radius-m);
  color: var(--ink-soft);
}
.title {
  font-size: var(--step-1);
  color: var(--ink);
}
.error .title {
  color: var(--fail);
}
.pulse {
  width: 2.5rem;
  height: 0.375rem;
  border-radius: 2px;
  background: var(--page);
  animation: pulse 1.2s ease-in-out infinite;
}
@keyframes pulse {
  50% {
    opacity: 0.25;
  }
}
@media (prefers-reduced-motion: reduce) {
  .pulse {
    animation: none;
  }
}
code {
  font-size: 0.9em;
  background: var(--paper-raised);
  padding: 0 0.25em;
  border-radius: var(--radius-s);
}
</style>
