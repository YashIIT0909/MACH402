import { PaymentError } from "@cleargate/client/browser";

/**
 * Turns a thrown value into something a renter can act on.
 *
 * `PaymentError` carries the node's own body, which is where the useful part
 * lives — "this node already has an active lease", "minutes must be between 5
 * and 120", "the lease image is not built yet". Showing only the status code
 * would throw all of that away.
 *
 * Everything else is reported as it actually arrived, including the error's
 * name and any `cause`. An earlier version pattern-matched "Failed to fetch"
 * and asserted the node was unreachable or misconfigured for CORS, which sent
 * at least one person checking a node that was answering correctly. A browser
 * raises that same bare TypeError for a blocked mixed-content request, an
 * extension-cancelled request, a DNS failure and a dropped connection, and this
 * code cannot tell those apart — so it no longer pretends to.
 *
 * Kept apart from `SessionFlow` so there is exactly one place that decides what
 * a renter reads when a payment or a node call fails.
 */
export function describe(caught: unknown): string {
  if (caught instanceof PaymentError) {
    return caught.body !== "" ? `${caught.message}\n${caught.body}` : caught.message;
  }
  if (caught instanceof Error) {
    const cause = caught.cause instanceof Error ? `\ncaused by: ${caught.cause.message}` : "";
    return `${caught.name}: ${caught.message}${cause}`;
  }
  return String(caught);
}
