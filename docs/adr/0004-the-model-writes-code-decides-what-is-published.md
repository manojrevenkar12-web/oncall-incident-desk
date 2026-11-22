# 0004 The model writes, code decides what is published

## Status

Accepted.

## Context

A model writes a clear first status post in a second. A person writes one after acknowledging, which in the
simulation is about fourteen minutes into the outage. But a model also makes mistakes:

- it promises recovery times;
- it guesses causes;
- it names components that work;
- it leaks host names.

A status page is a public promise.

## Decision

The model only writes words. The checker refuses an investigating post in any of these cases:

- it names a public component that is not affected;
- it names no affected one;
- it refers to time or quantity (beyond the start time the desk writes);
- it names a mechanism or cause;
- it contains anything that looks like an internal name.

The rules are categories, not phrases, so new wording of the same mistake still falls in a category. A refused
draft is replaced by the template built from the facts. A person's post goes through the same checker and is
refused with reasons, not silently replaced.

## Consequences

The first post goes out about one minute after the outage starts, with no false claims among the dev world's
drafts. The checker is still a rule list; the held-out section of the README reports what got past it.
