import { HeroSection } from "@/components/landing/hero-section";
import { FeaturesSection } from "@/components/landing/features-section";
import { HowItWorksSection } from "@/components/landing/how-it-works-section";
import { DevelopersSection } from "@/components/landing/developers-section";
import { CtaSection } from "@/components/landing/cta-section";

/*
 * Nothing on this page reads the registry any more — the price cards were the
 * last thing that did — so it prerenders as static content. Live node data
 * lives on /nodes, which stays dynamic because a listing goes stale the moment
 * a node stops beating.
 */
export default function HomePage() {
  return (
    <>
      <HeroSection />
      <FeaturesSection />
      <HowItWorksSection />
      <DevelopersSection />
      <CtaSection />
    </>
  );
}
