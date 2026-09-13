import { HeroSection } from "@/components/landing/hero-section";
import { McpSection } from "@/components/landing/mcp-section";
import { HowItWorksSection } from "@/components/landing/how-it-works-section";
import { DevelopersSection } from "@/components/landing/developers-section";
import { GradientWaves } from "@/components/landing/gradient-waves";

/*
 * Nothing on this page reads the registry any more — the price cards were the
 * last thing that did — so it prerenders as static content. Live node data
 * lives on /nodes, which stays dynamic because a listing goes stale the moment
 * a node stops beating.
 */
export default function HomePage() {
  return (
    <>
      {/*
       * The landing page's backdrop, and only the landing page's: it lives
       * here rather than in the layout so /nodes, /provide and /rent never
       * mount it.
       *
       * `fixed` and one viewport in size, so it stays behind the page as it
       * scrolls and costs one screen of fragments, not the document's height.
       * `-z-10` with no stacking context between here and the root puts it
       * above the body's ground and beneath everything in flow. Sections with
       * their own ground (how-it-works' panel) cover it; the rest show it.
       *
       * The values are the React Bits configurator's, one for one.
       */}
      <div aria-hidden="true" className="pointer-events-none fixed inset-0 -z-10">
        <GradientWaves
          horizonColor="#00c819"
          waveColor="#000000"
          crestColor="#ffffff"
          speed={0.4}
          amplitude={2.6}
          waveScale={0.6}
          waveRatio={0.7}
          swell={15}
          turbulence={8}
          tilt={1.3}
          zoom={0.4}
          height={3.4}
          fogDepth={12}
          detail="low"
          brightness={1}
          opacity={0.51}
          mouseInteraction
          parallaxStrength={0.38}
          grain
          grainIntensity={0.09}
        />
      </div>

      <HeroSection />
      <McpSection />
      <HowItWorksSection />
      <DevelopersSection />
    </>
  );
}
