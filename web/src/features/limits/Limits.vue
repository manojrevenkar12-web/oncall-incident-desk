<template>
  <section class="wrap block" aria-labelledby="limits-title">
    <h2 id="limits-title">What this does not show</h2>
    <ul>
      <li>The fleet, its outages, the people on call and their response times are simulated. The rates come from the configuration, not from a real customer.</li>
      <li>The model in the lab is a stand-in with stated error rates, not a language model. The desk can call Claude for drafts in production; the checker treats those words the same way.</li>
      <li>The held-out wording was written by the same person who wrote the checker's rules, so it is a harder test than the dev world but not a blind one.</li>
      <li>Results arrive once a minute from each location. Real check intervals, retries and alert channels vary by customer.</li>
    </ul>
    <p class="foot">
      Source, tests and the full report: <a href="https://github.com/manojrevenkar12-web/oncall-incident-desk">github.com/manojrevenkar12-web/oncall-incident-desk</a>
    </p>
  </section>
</template>

<style scoped>
.block {
  padding: var(--space-7) var(--space-5) var(--space-7);
}
h2 {
  font-size: var(--step-2);
  margin-bottom: var(--space-4);
}
ul {
  margin: 0;
  padding-left: var(--space-5);
  display: grid;
  gap: var(--space-2);
  color: var(--ink-soft);
  max-width: var(--measure);
}
.foot {
  margin-top: var(--space-5);
  color: var(--ink-muted);
  font-size: var(--step--1);
}
</style>
