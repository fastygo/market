# ADR-0012: Design principles

- Status: Proposed

## Decision

Order of priority when they conflict: correctness of money > KISS > the rest.

- KISS and YAGNI first: build the phase in front of us. Data model is ready
  for multi-vendor (cheap now, expensive later); code paths are not.
- DRY for knowledge, not for text: one source for prices (offers), status
  enums, money math, VAT, capabilities, UI tokens and dictionary keys.
  Templates and handlers may repeat until the third copy (rule of three).
- SOLID, the Go way:
  - single responsibility = one bounded context per package;
  - open/closed only where variation is real (payment gateways, receipt
    builders);
  - interfaces are small and declared by the consumer (interface segregation,
    dependency inversion); return structs, accept interfaces.
- OOP: no class hierarchies or inheritance. Structs with methods guarding
  invariants (`Order.Apply(event)`), composition, value objects (`Money`,
  `PurchaseCode`), typed enums.
- Patterns we use: ports and adapters (lite), state machine, transactional
  outbox and inbox, idempotency keys, append-only ledger, snapshot on write,
  repository per aggregate, view model per screen.
- Patterns we avoid: generic repositories, service locators, event sourcing,
  CQRS buses, microservices, ORMs beyond PocketBase.
- Errors: wrap with `%w`, user-facing issues use Codex `validation` codes.
- Logging: `slog`, structured, no personal data.

## Consequences

Reviewers can reject a change citing a principle by name; new abstractions
need a second real use.
