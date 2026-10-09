# 0004 - Name: Aotus. License: Apache-2.0

Status: accepted
Date: 2026-10-09

## Context

The project needed a definitive name and a license before any product code. Requirements for the name: short, easy to type as a command, not tied to a single metaphor (the first ideas were too pirate-themed and said nothing about agents working), and free enough on package registries and domains.

The product runs a team of agents that keep working in the background while the user does something else or sleeps.

## Decision

- **Name: Aotus.** Aotus is the genus of night monkeys, the only nocturnal monkeys: they work while you sleep. It also sounds like "auto-s" (automation), and a big-eyed monkey is an easy mascot. A group of monkeys is a troop, which fits a crew of agents, and capuchin and macaque monkeys use tools, as agents do. Names derived from this: daemon `aotusd`, command line tool `aotus`, desktop app `aotus-desktop`, Go module `aotus`, data directory `~/.aotus`.
- **License: Apache-2.0.** It allows commercial use, includes an explicit patent grant and patent-retaliation clause, and is widely accepted by companies. The full text is in `LICENSE`. MIT was the alternative: simpler, but without the patent grant.

## Evidence (checked 2026-10-09)

Package names `aotus` on npm, PyPI and crates.io were not registered. The domains aotus.dev, aotus.app, aotus.io and aotus.ai were not registered at their registries (RDAP queries). The `.sh` and `.com` results were not reliable. A GitHub user named `aotus` may exist as it does for almost every short word, so the organization will probably be `aotus-dev` or similar.

## Consequences

- **Open items, not done yet:** trademark search (USPTO and EUIPO, software classes), buying the domains and reserving the package names, creating the GitHub organization. These are the owner's actions.
- Copyright holder in the license headers and any `NOTICE` file must be set by the owner when the repository is published.
- The earlier working name `OpenCrew` is gone from the repository; the module path changed from `opencrew` to `aotus`.
- Rejected names: Marque and Deckhand (taken on GitHub, npm and PyPI; Deckhand also had a very similar AI project), Zarpa, Grumete, Kaper, Strogoff and others (viable but less fitting).
