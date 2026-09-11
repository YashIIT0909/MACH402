import type { Metadata } from "next";
import { IBM_Plex_Sans, JetBrains_Mono } from "next/font/google";

import { Navigation } from "@/components/landing/navigation";
import { FooterSection } from "@/components/landing/footer-section";

import "./globals.css";

/*
 * Two typefaces, with one job each.
 *
 * IBM Plex Sans carries everything a person reads as prose — headings, ledes,
 * paragraphs. JetBrains Mono carries everything a machine produced or that
 * labels something: node ids, Hedera accounts, tinybar amounts, image tags,
 * navigation, section labels. That split is the site's oldest rule and the
 * reason it is legible at a glance which values are data.
 *
 * Plex is the deliberate pair for Plex Mono's sibling; it sits beside JetBrains
 * Mono without either looking borrowed, and it holds up at the small sizes the
 * tables need.
 */
const sans = IBM_Plex_Sans({
  subsets: ["latin"],
  weight: ["400", "500", "600"],
  variable: "--font-plex",
  display: "swap",
});

const mono = JetBrains_Mono({
  subsets: ["latin"],
  weight: ["400", "500"],
  variable: "--font-jetbrains",
  display: "swap",
});

export const metadata: Metadata = {
  title: "ClearGate — rent the machine, pay the machine",
  description: "Rent GPU time by the job, settled with x402 payments on Hedera testnet.",
};

export default function RootLayout({ children }: { children: React.ReactNode }) {
  return (
    /*
     * The font variables go on <html>, not <body>: the face stacks in
     * globals.css are declared on :root, and a custom property there cannot
     * resolve one that is only defined on a descendant. With them on <body>
     * every one of those stacks computed to nothing and inherited the body
     * face instead.
     */
    <html lang="en" className={`${sans.variable} ${mono.variable}`}>
      <body className="font-sans antialiased">
        {/*
          * `overflow-x: clip`, not `hidden`. Both stop sideways scrolling, but
          * `hidden` makes this a scroll container, which silently kills
          * `position: sticky` for everything inside it — including the pinned
          * hero. `clip` has no such side effect.
          */}
        <div className="noise-overlay relative min-h-screen overflow-x-clip">
          <Navigation />
          <main>{children}</main>
          <FooterSection />
        </div>
      </body>
    </html>
  );
}
