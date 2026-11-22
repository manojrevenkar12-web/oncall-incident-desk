# 0002 Locations confirm, and a suspect location is left out

## Status

Accepted.

## Context

There are two cheap fixes for noise:

- "two failures in a row";
- "retry from a second location".

The first pages on every blip of a monitoring location's network. The second never pages on a real regional
outage, because only one location ever fails.

## Decision

A check is down in either of two cases:

- two locations are failing with at least two failed runs between them;
- one location has failed four runs in a row. This is a regional incident, marked minor.

A location is left out for three minutes when two conditions hold in the same minute:

- 60% or more of its checks fail;
- at most 15% fail elsewhere.

When every location fails, nothing is filtered.

## Consequences

Blips stop paging. In the dev world, the ablation without the filter has about 20 times the false pages.
Regional outages are still caught, about four minutes late.
