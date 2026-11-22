# 0005 The held-out world runs once

## Status

Accepted.

## Context

Every threshold was chosen while looking at the dev numbers. Those numbers are therefore optimistic.

## Decision

A second world differs from the dev world:

- other seeds;
- four locations;
- more outages, blips, flakes and duplicates;
- slower nights;
- status-post errors worded differently.

It runs once. The run writes `reports/heldout.lock`, the command then refuses to run again, and CI checks
that it refuses. Its numbers, including failed gates, go into the README as they came out.

## Consequences

The README carries one honest test of generalisation. The wording was written by the same person who wrote the
checker, so it is a harder test, not a blind one. The limits section says so.
