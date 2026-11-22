# 0003 One cause pages one person

## Status

Accepted.

## Context

When a dependency fails, every service that uses it fails too. Per-check alerting turns one outage into a dozen
pages, sent to four teams.

## Decision

Down checks group into one incident per service. An incident becomes a child of a failing dependency's open
incident, found by walking the catalogue's `depends_on` transitively. This happens in two cases:

- the incident opens while the dependency is already failing;
- the dependency's incident opens within ten minutes after it.

Children do not escalate. A child still failing two minutes after its root recovers becomes a root and pages its
own team.

## Consequences

Pages per outage halve against the desk without grouping. In the dev world, grouping costs about half a minute
of median time to acknowledge: fewer pages give fewer chances that someone answers early. The escalation policy
pays that back. Primary again at three minutes, secondary at six and the manager at fifteen.
