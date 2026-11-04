# 0002. Modular monolith, one binary with roles

- Status: accepted
- Date: 2026-11-04
- Deciders: maintainer

## Context and problem statement

getafjob has a web UI, background jobs (crawl, matching, rules, inbox, notifications), browser runners and
administration. It is operated by one person on one server, but runners must scale and be isolated.

## Decision drivers

- Simple operation and releases (one artifact, one version).
- Runners isolated from data; web and workers scale independently.
- Refactoring across modules stays cheap while the domain settles.

## Considered options

1. Microservices
2. Modular monolith, one binary with roles
3. Classic monolith (one process does everything)

## Decision outcome

Chosen option: "Modular monolith, one binary with roles", because it keeps one build and one version while
processes stay separate: `serve` (web UI, API v1, SSE, runner gateway), `worker` (queue jobs), `runner`
(browser), `migrate` and `admin`. Each role is its own container; the home runner is the same binary.

### Consequences

- Good, because there is one release, one dependency set and one test suite.
- Good, because roles get their own resources, privileges and network access.
- Bad, because module boundaries must be enforced by tooling ([0003](0003-ports-and-adapters-module-layout.md)), not
  by the network.
