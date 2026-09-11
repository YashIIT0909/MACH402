import type { Metadata } from "next";
import { Instrument_Sans, Instrument_Serif, JetBrains_Mono } from "next/font/google";

import { Navigation } from "@/components/landing/navigation";
import { FooterSection } from "@/components/landing/footer-section";

import "./globals.css";

/*
 * Three typefaces, exposed to globals.css as CSS variables: a serif for display
 * headings, a sans for prose, and a monospace for machine-produced values —
 * node ids, Hedera accounts, tinybar amounts, image tags.
 */
const sans = Instrument_Sans({
  subsets: ["latin"],
  variable: "--font-instrument",
  display: "swap",
});

const serif = Instrument_Serif({
  subsets: ["latin"],
  weight: "400",
  variable: "--font-instrument-serif",
  display: "swap",
});

const mono = JetBrains_Mono({
  subsets: ["latin"],
  variable: "--font-jetbrains",
  display: "swap",
});

export const metadata: Metadata = {
  title: "ClearGate — rent the machine, pay the machine",
  description: "Rent GPU time by the job, settled with x402 payments on Hedera testnet.",
};

export default function RootLayout({ children }: { children: React.ReactNode }) {
  return (
    <html lang="en">
      <body
        className={`${sans.variable} ${serif.variable} ${mono.variable} font-sans antialiased`}
      >
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
