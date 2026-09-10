import type { Metadata } from "next";
import Link from "next/link";

import "./globals.css";

export const metadata: Metadata = {
  title: "ClearGate",
  description: "Rent GPU time, settled with x402 payments on Hedera testnet.",
};

export default function RootLayout({ children }: { children: React.ReactNode }) {
  return (
    <html lang="en">
      <body>
        <nav>
          <strong>ClearGate</strong>
          <Link href="/nodes">Nodes</Link>
          <Link href="/provide">List your GPU</Link>
        </nav>
        <main>{children}</main>
      </body>
    </html>
  );
}
