Implementation plan — metered sessions via repeated x402 payments, with per-tick refund transparency

This replaces the current contract-verified session flow (agent/internal/httpapi/sessions.go + agent/internal/escrow) with sessions funded the same way leases already are — s.collectPayment against the facilitator, repeated — and adds the mitigation you picked: the amount owed back to the renter if the node stopped right now is published to HCS on every meter tick, not just at close.

Phase 0 — types (packages/types)

- SessionSpec: unchanged shape (seconds, public_key, require_gpu).
- SessionQuote: drop escrow_contract, provider_address as contract terms. Keep price_tinybars_per_second, min_seconds, max_seconds, max_total_seconds, network.
- SessionCreated: drop deposit_transaction, deposited_tinybars. Add credit_tinybars, price_tinybars_per_second (already there), low_credits: bool.
- One PR touching this package and both agent/ and client/ consumers together, per the existing "changing the 402 challenge shape is a cross-team break" rule.

Phase 1 — runner.Lease data model (agent/internal/runner/lease.go)

- Replace MarkEscrow(sessionID, proof, expiresAt, duration) with MarkMetered(sessionID string, pricePerSecond, creditTinybars *big.Int).
- Add fields: CreditTinybars *big.Int, PricePerSecond *big.Int, Burned *big.Int (cumulative, for the audit trail), LastTickAt time.Time.
- Add AddCredit(amount *big.Int) for top-ups (mirrors ExtendPaidUntil for direct leases).
- Add SecondsRemaining() int64 computed as CreditTinybars / PricePerSecond instead of reading an on-chain expiresAt.
- Drop DepositTransaction(), PaidSeconds() (escrow-specific reads) once nothing calls them.

Phase 2 — handleCreateSession (agent/internal/httpapi/sessions.go)

Rewrite to match handleCreateLease's shape exactly:

amount, err := s.sessionOpeningPrice(spec.Seconds)   // price-per-second × seconds, capped at a chunk max (mitigation #1)
payload, requirements, ok := s.collectPayment(w, r, amount, s.sessionDescription(spec.Seconds))

- Delete HeaderDepositProof, PAYMENT-SESSION header handling, s.escrow.AwaitDeposit.
- After collectPayment succeeds: start container → sign cert → tunnel → reachability check, identical to leases. On success, settle through the facilitator (s.settle, same call handleCreateLease makes) and call lease.MarkMetered(sessionID, pricePerSecond, big.NewInt(mustParse(requirements.Amount))) — credit is exactly what the facilitator confirmed, never a price table.
- sessionOpeningPrice caps the requested seconds at a configurable chunk (leases.session_chunk_seconds, new config field, default e.g. 300s) regardless of what the renter asked for — this is mitigation #1 from the earlier discussion, and it bounds how much can ever be at risk in the trust gap this plan is patching.

Phase 3 — handleTopUpSession

- Same rewrite: call s.collectPayment again against the same lease id and chunk size, then lease.AddCredit(...). Delete s.escrow.AwaitTopUp and the ErrNoAdditionalTime/mirror-lag handling — there's no mirror-node read anymore, so no lag to wait out.
- sessionState() computes low_credits as SecondsRemaining() < lowCreditThreshold (new const, e.g. 60s) and returns it so the renter's client knows to fire the next top-up before hitting zero.

Phase 4 — meter tick, folded into the existing sweep (agent/internal/httpapi/leases.go: sweepLeases)

No new ticker — leaseSweepInterval (15s) already exists. Add, inside sweepLeases, before the existing LeaseActive/LeasePaused switch, a branch for metered leases:

if lease.SessionID() != "" {
    s.tickMeter(ctx, lease)
}

tickMeter:
1. Compute elapsed := time.Since(lease.LastTickAt), burned := PricePerSecond * elapsed.Seconds().
2. lease.CreditTinybars -= burned, lease.Burned += burned, lease.LastTickAt = now.
3. If CreditTinybars <= 0: clamp to zero and fall through to the existing freeze path (PauseLease) — same mechanism direct leases already use, no new code.
4. This is where the mitigation lands: publish the running refund-owed figure —

s.publishAudit(hcs.AuditMessage{
    Kind:            hcs.KindSessionBurn,
    SessionID:       lease.SessionID(),
    PricePerSecond:  lease.PricePerSecond.String(),
    ElapsedSecs:     ..., // cumulative burned seconds
    RefundTinybars:  lease.CreditTinybars.String(), // what's owed back RIGHT NOW if stopped
})

This runs unconditionally every 15s a metered lease is active, not just at open/close — that's the actual mitigation: the "owed if stopped now" number becomes a public, consensus-ordered, running-hash-bound fact well before any dispute, so a provider who later refuses to refund has already published the number they're refusing to honor.

Phase 5 — close / stop (handleSessionStop, settleSession)

- recordSessionSettlement's math is already correct (earned, refund) — no change to the arithmetic, only to where the money for refund comes from.
- New method on the hedera sidecar (agent/internal/hedera): Refund(ctx, to string, tinybars *big.Int) (string, error) — a plain CryptoTransfer, signed with the same operator key that already signs SettleSession. This is the sidecar-runs-as-child-process pattern already established; no new key surface.
- settleSession calls s.sidecar.Refund(...) for lease.CreditTinybars (the unburned remainder) before publishing KindSessionSettled. If s.sidecar == nil (no self_settle), log the owed amount explicitly and rely on the Phase 4 trail as the record of what's owed — same "not an error, permissionless close cannot exist here" framing settleSession already uses for on-chain settle today, just pointed at a different failure mode.

Phase 6 — hcs.go

- Add KindSessionBurn = "session_burn" alongside the existing kinds.
- AuditMessage already has every field this needs (PricePerSecond, ElapsedSecs, RefundTinybars) — no struct changes required, just a new Kind value and a new call site.

Phase 7 — client (client/src)

- Delete whatever currently builds openSession/topUp contract calls (Hedera SDK contract-execute path for escrow).
- The existing x402 payer now targets /v1/sessions and /v1/sessions/{id}/topup the same way it already targets /v1/leases — no new client-side payment logic, since both are now plain x402 exact-scheme cycles.
- Add a small watch loop in the browser session flow: poll or read the SSE state, and when low_credits: true appears, pay /topup through a wallet prompt (reusing the existing wallet-signing work from the recent feat(client): sign x402 payments from a browser wallet commit).

Phase 8 — config (agent/internal/config)

- Add leases.session_chunk_seconds (int, default 300) and leases.low_credit_threshold_seconds (int, default 60).
- leases.payment_mode: session keeps its name and its opt-in gate (sessionsEnabled stays 404-by-default); only what happens inside changes.

Phase 9 — retire or park agent/internal/escrow and contracts/SessionEscrow.sol

Per the earlier discussion, don't delete outright — leave the contract and package in the repo, unused by the default path, and note in the README that it's the harder-guarantee fallback that a future payment_mode: escrow-vault could reactivate. Deleting it now forecloses that option for no benefit; keeping it costs nothing since it's not wired into any route once Phases 2–3 land.

Phase 10 — tests

- agent/internal/httpapi: rewrite session tests to drive handleCreateSession/handleTopUpSession through the facilitator mock the lease tests already use (s.collectPayment's test double), dropping the mirror-node/escrow mocks.
- New test: sweepLeases ticks a metered lease down and asserts publishAudit was called with KindSessionBurn and the correct RefundTinybars at each tick — the thing this whole mitigation depends on actually firing.
- make smoke-agent extended (or a new make smoke-session) to run one real open → tick → top-up → stop cycle against testnet, checking the HCS topic has the burn checkpoints.

Order to actually build it in

Phases 0–2 first (get a session opening via plain x402 working end to end), then 3–4 (meter + refund-transparency, the point of this change), then 5 (the refund transfer itself), 6 is trivial and can ride along with 3, then 7–8, with 9 as a documentation-only step and 10 threaded throughout rather than saved for the end.