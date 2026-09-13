import type { NodeListing } from "@cleargate/types";
import { SessionFlow } from "./session-flow";

/**
 * The interactive-rental flow for a node, or the reason there is not one.
 *
 * Interactive time is sold only as a metered session: a credit the node burns
 * down by the second and refunds the remainder of. A node that advertises any
 * other `leases.payment_mode` is running a build from before that, which sold
 * prepaid minutes with no refund — this site does not buy those, so it says so
 * rather than offering a purchase that works differently from every other one.
 */
export function RentFlow({ node }: { node: NodeListing }) {
  const offer = node.leases;

  if (offer === undefined) {
    return (
      <Notice title="This node is not selling right now">
        Its provider has sessions switched off, so there is nothing to rent here — and nothing you
        do on this page would be charged.
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

  if (offer.payment_mode !== "session") {
    return (
      <Notice title="This node runs an older ClearGate build">
        It still sells prepaid minutes with no refund, which this site no longer buys. Sessions here
        are metered by the second and refund whatever you do not use, so this node becomes rentable
        once its provider updates.
      </Notice>
    );
  }

  return <SessionFlow node={node} offer={offer} />;
}

function Notice({ title, children }: { title: string; children: React.ReactNode }) {
  return (
    <div className="border border-foreground/10 p-6">
      <h3 className="mb-3 text-lg">{title}</h3>
      <p className="text-sm text-muted-foreground">{children}</p>
    </div>
  );
}
