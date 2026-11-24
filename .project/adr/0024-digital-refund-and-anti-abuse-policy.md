# ADR-0024: Digital goods refund and anti-abuse policy (region RefundPolicy)

- Status: Proposed (owner approval t234u, 2026-11-24; docs only). Based on
  an internal legal memo of 2026-11-24 (not in the repo; "not a legal
  opinion"). Points marked **needs counsel** must be confirmed by a
  practicing lawyer before the public offer and license texts are
  published.

## Context

Buyers download a copy of a template, theme, module or design; a refund
cannot take the copy back. The engine already has full refunds as a domain
command, entitlement `refunded`, closed `/dl` links and ledger reversal
(ADR-0007, ADR-0009, ADR-0021). What is missing is the rule that says when
a buyer is entitled to a refund, how the decision is recorded, what
evidence is kept for chargebacks, and how abuse is handled without refusing
lawful refunds. The rule differs by region, so it is a region pack
interface; the core holds no RU numbers.

## Legal basis for the RU pack (as assessed in the memo)

- Unified Rules on consumer protection approved by Decree No. 6 of the
  Supreme State Council of the Union State of 2024-12-06, chapter IV
  "digital product" (Art. 43 to 48): Art. 48 gives a consumer the right to
  withdraw from a digital product of proper quality within 7 days, not
  counting the day of purchase, with a full refund; the open text has no
  "after download" exception. Art. 43(1): access is provided immediately
  unless agreed otherwise. Art. 44: on a defect the consumer chooses the
  remedy (fix, replacement, price reduction, or withdrawal with refund).
  Art. 45: defect claims within the warranty or, without one, a reasonable
  period up to 2 years from access. Art. 46(4): refund within 7 days of the
  claim. Art. 13: list of void terms. **Needs counsel**: the preamble calls
  the Rules a "framework"; Rospotrebnadzor (clarifications of 2025-04-07,
  2025-05-07, 2025-08-20) treats them as directly applicable and prevailing
  over national law where they conflict; the engine follows the agency
  position until counsel says otherwise.
- Consumer Protection Law No. 2300-1 of 1992-02-07: Art. 16 (void
  consumer terms; list added by Federal Law No. 135-FZ of 2022-05-01,
  in force 2022-09-01), Art. 18 and 22 (defects, 10-day refund term),
  Art. 26.1 (distance sale, 7 days; up to 3 months if the return
  procedure was not given in writing), Art. 32 (services: withdrawal at
  any time, paying actual costs). Terms such as "no refund after download"
  or "all sales final" within the withdrawal period are likely void.
- Civil Code: Art. 1235, 1286 (simple non-exclusive license), Art. 1237,
  1270, 1301 (use after the license ends infringes the exclusive right),
  Art. 1299 (technical protection measures), Art. 426 (public contract:
  no unjustified refusal to contract; **needs counsel** for any purchase
  restriction), Art. 196 (3-year limitation), Art. 434, 438 (acceptance).
- Federal Law No. 63-FZ, Art. 6 and 9 (simple electronic signature
  agreement for acceptance); Federal Law No. 152-FZ, Art. 3 and 6 (an
  order id linked to an account is personal data; processing on contract
  and legitimate interest grounds).
- Government Decree No. 657 of 2026-05-30 (retail sale rules): software
  and digital goods are not on the non-returnable list (per the memo;
  **needs counsel** to confirm).
- Platform economy laws No. 289-FZ and No. 290-FZ of 2025-07-31: may not
  apply while only the operator sells; with third-party sellers the
  platform may need a claims and refund channel. **Needs counsel.**
- Court practice: one district court decision (case No. 2-1305/2025)
  refused to apply "license fee is non-refundable" to a consumer software
  contract; first instance only, different facts, not a precedent.
- Business buyers: Art. 48 is a consumer right; a separate B2B contract
  without it is possible, but a sole proprietor may still be a consumer if
  the business purpose is not proven. **Needs counsel.**

## Decision

### Policy (the RU pack defaults; values are config of the pack)

1. Before the first download: refund on request, simplified (service
   standard, no questions).
2. After download, within the cooling-off window (RU default 7 days, not
   counting the purchase day): full refund of a product of proper quality.
   Together: license terminated, entitlement `refunded`, download links
   closed, notice of termination and a request to delete copies sent and
   kept as evidence. No deduction of "costs" for a standard file.
3. After the window, proper quality: refund refused, with the product
   description, demo and access evidence attached to the decision.
   Voluntary (goodwill) refunds remain possible and may be limited per
   account.
4. Defect: the claim is accepted within the defect window (RU default 2
   years from access, or a longer warranty); the seller is offered a short
   period to fix; if not fixed or not fixable, replacement or refund. The
   buyer's choice of remedy is never removed; "repair only" is not a rule.
   Fix period in days: **needs counsel** when the offer is approved.
5. Before purchase: demo or preview, full file list, compatibility,
   license text, and an explicit notice of the cooling-off right and of the
   watermark. The same notice is repeated in the purchase email and in the
   account (Art. 26.1 risk).
6. Refund money is returned within the region's deadline (RU: one term of
   at most 7 days written in the offer, covering both Art. 46(4) and Art.
   22; **needs counsel**).

### Engine: `RefundPolicy` port (declared in `ordering`)

```go
type RefundClaim struct {
    Kind          ClaimKind // withdrawal, defect, goodwill
    PaidAt        time.Time // and the storefront time zone for day counting
    AccessAt      time.Time // first time the download became available
    FirstDownload *time.Time
    Now           time.Time
    BuyerKind     string    // consumer, business (from the order snapshot)
    DefectState   string    // none, reported, fix_offered, fixed, unfixable
}
type RefundDecision struct {
    Eligible       bool
    Basis          string    // pre_download, cooling_off, defect, goodwill, none
    Remedies       []string  // refund, replace, fix, price_reduction
    Deadline       time.Time // when the money must be returned
    ManualReview   bool
    PolicyVersion  string
}
type RefundPolicy interface { Evaluate(c RefundClaim) RefundDecision }
```

- Pure and deterministic (no network, no ctx), evaluated outside write
  transactions; the decision (basis, policy version) is snapshotted on the
  refund request and the refund. Windows are pack config: cooling-off days,
  whether the purchase day counts, day boundary time zone, defect window,
  refund deadline days, goodwill limit.
- No pack configured: only operator refunds (today's behaviour). The core
  never contains RU numbers; tests use a fake policy with other values.
- Refund requests: `refund_requests` (order_item, buyer, kind, message,
  evidence files, status `submitted`,`approved`,`rejected`,`remedy_offered`,
  `escalated`,`withdrawn`, decision basis, policy_version, deadline_at,
  decided_by). Eligible `pre_download` and `cooling_off` requests are
  approved automatically and run the existing refund command; defects and
  goodwill go to the operator queue. Refund reason codes add
  `cooling_off`, `defect`, `goodwill` (with `pre_download` as a basis).
- Marketplace: the refund is attributed to the seller (ADR-0021); the
  platform is the refund channel for buyers in every case.

### Evidence and logs (chargebacks and claims)

- `access_evidence` (append-only, Go-only, no update/delete): user,
  order, order_item, entitlement, event (`access_granted`,
  `download_link_issued`, `download_redirected`, `license_terminated`,
  `notice_sent`), release, file sha256, watermark id, ip, user agent,
  offer and license versions accepted, at. Retention at least 5 years
  (pack config), then purge by a job; access limited to superusers; ip and
  user agent are personal data, kept for this purpose only (152-FZ notice in
  the privacy policy). The existing hashed `downloads` log stays for
  analytics. Download completion is not observable by the app (files are
  served by storage); storage access logs can be added if the provider
  offers them.
- Acceptance: order `terms_version` plus `consents` with versions (simple
  electronic signature agreement in the offer).
- Evidence pack: an operator command builds a bundle per order (order,
  payment, tax document, acceptance, access events, file hash, emails sent)
  for a chargeback or claim; built outside transactions, stored privately.

### Watermark

- A per-order watermark is a neutral random id (never name, email or
  phone) mapped to the order in the database; the mapping is personal data
  and stays in the database only. Embedding is a capability of the product
  type `Delivery` (ADR-0019): for example a license file with the id inside
  the archive, or an id in file metadata. Producing per-order copies needs
  a background job and per-order storage objects; the technique and its
  cost on the target host are an open question.

### Anti-abuse

- Signals (refund count and rate per account, download-then-refund within
  the window, watermark found in a leak, payment method reuse) feed
  `risk_reviews` for manual review. A signal never refuses or delays a
  lawful refund (cooling-off or defect) and never shortens its deadline.
- Goodwill refunds may be limited per account by config.
- Restricting future purchases of an account after proven abuse is an
  open question (terms of service and Art. 426); until decided the engine
  only flags.

## Collections (new or changed)

| Collection | Fields | Unique / invariants |
| --- | --- | --- |
| `refund_requests` | order_item, order, seller, buyer, kind (`withdrawal`,`defect`,`goodwill`), message, evidence (files), status, basis, remedies (json), policy_version, deadline_at, decided_by, decided_at, refund | one open request per order item; Go-only transitions |
| `refunds` (changed) | + basis, policy_version; reason_code adds `cooling_off`, `defect`, `goodwill` | as ADR-0007 |
| `access_evidence` | see above | append-only; retention by config (RU >= 5 years) |
| `watermarks` | order_item, token (random), product, release, created | token unique |
| `risk_reviews` | user, signal codes (json), status (`open`,`cleared`,`actioned`), note, handled_by | one open review per user |
| `orders` (changed) | + buyer_kind (`consumer`,`business`) | snapshot at checkout |

## Consequences

- The legally sensitive numbers live in the region pack and its config;
  another region changes them without touching the core.
- Lawful refunds are fast and automatic; abuse is handled by evidence,
  watermarks and manual review, not by refusing rights.
- Resource fit (1 vCPU / 2 GB): evidence rows are small appends on events
  that already write; the evidence pack and watermark jobs run in the
  background one at a time; the 5-year log grows by a few rows per order.
