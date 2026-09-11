import type { NodeListing } from "@cleargate/types";

import { fetchNodes, sortByPrice, summarize } from "@/lib/registry";
import { HeroSection } from "@/components/landing/hero-section";
import { FeaturesSection } from "@/components/landing/features-section";
import { HowItWorksSection } from "@/components/landing/how-it-works-section";
import { PartiesSection } from "@/components/landing/parties-section";
import { MetricsSection } from "@/components/landing/metrics-section";
import { StackSection } from "@/components/landing/stack-section";
import { SecuritySection } from "@/components/landing/security-section";
import { DevelopersSection } from "@/components/landing/developers-section";
import { AuditSection } from "@/components/landing/audit-section";
import { PricingSection } from "@/components/landing/pricing-section";
import { CtaSection } from "@/components/landing/cta-section";

/*
 * The counters and the price cards read the registry, and a listing goes stale
 * the moment a node stops beating — so this page is never cached either.
 *
 * A registry that is down is not an error here. `fetchNodes` reports the
 * failure instead of throwing, the metrics fall back to the protocol constants
 * and the price cards say so. Nodes keep selling jobs either way.
 */
export const dynamic = "force-dynamic";

/** How many price cards the pricing section shows. */
const PRICE_CARDS = 3;

export default async function HomePage() {
  const result = await fetchNodes();
  const nodes = result.ok ? result.nodes : [];

  return (
    <>
      <HeroSection />
      <FeaturesSection />
      <HowItWorksSection />
      <PartiesSection />
      <MetricsSection snapshot={result.ok ? summarize(nodes) : null} />
      <StackSection />
      <SecuritySection />
      <DevelopersSection />
      <AuditSection />
      <PricingSection nodes={forSale(nodes).slice(0, PRICE_CARDS)} />
      <CtaSection />
    </>
  );
}

/** Nodes a renter could actually pay right now, cheapest first. */
function forSale(nodes: NodeListing[]): NodeListing[] {
  return sortByPrice(nodes.filter((node) => node.online && !node.paused));
}
