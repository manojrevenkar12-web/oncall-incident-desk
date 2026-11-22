# 0001 A Go core behind one store contract

## Status

Accepted.

## Context

The posting names Go and Postgres. The lab has to play millions of check results per design per seed, and the
server has to survive restarts with incidents still open.

## Decision

The desk is a plain Go engine with two inputs:

- `Ingest(result)`
- `Tick(now)`

It produces pages through a `Notifier`, and incidents and posts through a `Store`. Postgres and memory
implement one contract, tested against both. The lab uses memory. The server uses Postgres.

`desk replay` plays six hours into both stores and requires identical decisions. `Load` rebuilds open
incidents and each check's state after a restart.

## Consequences

The lab and the server run the same code path. A store bug that changes a decision fails CI. Memory keeps only
two hours of results; Postgres keeps them all.
