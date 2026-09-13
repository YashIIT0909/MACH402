import type { Metadata } from "next";
import { Atkinson_Hyperlegible_Mono, Atkinson_Hyperlegible_Next } from "next/font/google";
import localFont from "next/font/local";

import { Navigation } from "@/components/landing/navigation";
import { FooterSection } from "@/components/landing/footer-section";

import "./globals.css";

/*
 * Two families, with one job each. The reasoning lives beside the type scale in
 * globals.css; what matters here is how each one is loaded.
 *
 * Ticketing is self-hosted: it is not on Google Fonts. It is K-Type's, and its
 * free release is for personal use — K-Type's licence says free fonts used
 * "as webfonts, need to be licensed", so a public deployment of this site needs
 * one bought from k-type.com. Converted to WOFF2 (118KB to 25KB), otherwise
 * untouched.
 */
const ticketing = localFont({
  src: "./fonts/Ticketing.woff2",
  weight: "400",
  style: "normal",
  variable: "--font-ticketing",
  display: "swap",
});

/*
 * `adjustFontFallback: false` on both Atkinson cuts: Next ships no metric
 * overrides for them, and without this it logs a failure on every compile and
 * builds no adjusted fallback anyway. Saying so here makes that a decision.
 */
const atkinson = Atkinson_Hyperlegible_Next({
  subsets: ["latin"],
  variable: "--font-atkinson",
  display: "swap",
  adjustFontFallback: false,
});

const atkinsonMono = Atkinson_Hyperlegible_Mono({
  subsets: ["latin"],
  variable: "--font-atkinson-mono",
  display: "swap",
  adjustFontFallback: false,
});

export const metadata: Metadata = {
  title: "MACH402 — rent the machine, pay the machine",
  description: "Rent GPU time by the second, settled with x402 payments on Hedera testnet.",
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
    <html lang="en" className={`${ticketing.variable} ${atkinson.variable} ${atkinsonMono.variable}`}>
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
