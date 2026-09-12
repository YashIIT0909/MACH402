import type { NodeListing } from "@cleargate/types";
import { LeaseFlow } from "./lease-flow";
import { SessionFlow } from "./session-flow";

/**
 * Picks which interactive-rental flow a node actually sells and renders it.
 *
 * The split is not a display variant of one flow — `leases.payment_mode`
 * decides what a payment *is*. `"direct"` (or a node old enough not to say
 * either way) buys minutes that are gone whether used or not; `"session"` buys
 * a metered credit the node burns down and refunds the remainder of. Those are
 * different enough in what "stop" means, what a renter reads while it runs, and
 * what backs the promise that keeping them as one component with branches
 * throughout would be harder to read than two components with one branch here.
 */
export function RentFlow({ node }: { node: NodeListing }) {
  const offer = node.leases;

  if (offer === undefined) {
    return (
      <Notice title="This node does not sell interactive time">
        It runs batch jobs only. Leasing is opt-in per provider and must never be switched on as a
        side effect of anything else, so a node without it is a normal node rather than a
        misconfigured one.
      </Notice>
    );
  }

  if (!node.online) {
    return (
      <Notice title="This node is offline">
        It has either withdrawn or missed its heartbeats. Nothing can be rented until it checks in
        again — and nothing you do here would be charged.
      </Notice>
    );
  }

  return offer.payment_mode === "session" ? (
    <SessionFlow node={node} offer={offer} />
  ) : (
    <LeaseFlow node={node} offer={offer} />
  );
}

function Notice({ title, children }: { title: string; children: React.ReactNode }) {
  return (
    <div className="border border-foreground/10 p-6">
      <h3 className="mb-3 text-lg">{title}</h3>
      <p className="text-sm text-muted-foreground">{children}</p>
    </div>
  );
}
