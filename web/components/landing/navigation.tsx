"use client";

import { useEffect, useState } from "react";
import Link from "next/link";
import { Menu, X } from "lucide-react";

import { Button } from "@/components/ui/button";

const navLinks = [
  { name: "Nodes", href: "/nodes" },
  { name: "How it works", href: "/#how-it-works" },
  { name: "Security", href: "/#security" },
  { name: "Developers", href: "/#developers" },
];

export function Navigation() {
  const [isScrolled, setIsScrolled] = useState(false);
  const [isMobileMenuOpen, setIsMobileMenuOpen] = useState(false);

  useEffect(() => {
    const handleScroll = () => setIsScrolled(window.scrollY > 20);
    handleScroll();
    window.addEventListener("scroll", handleScroll, { passive: true });
    return () => window.removeEventListener("scroll", handleScroll);
  }, []);

  // A fixed body behind an open full-screen menu scrolls under it otherwise.
  useEffect(() => {
    document.body.style.overflow = isMobileMenuOpen ? "hidden" : "";
    return () => {
      document.body.style.overflow = "";
    };
  }, [isMobileMenuOpen]);

  return (
    <header
      className={`fixed z-50 transition-all duration-500 ${
        isScrolled ? "top-4 right-4 left-4" : "top-0 right-0 left-0"
      }`}
    >
      <nav
        className={`mx-auto transition-all duration-500 ${
          isScrolled || isMobileMenuOpen
            ? "max-w-[1200px] rounded-2xl border border-foreground/10 bg-background/80 shadow-lg backdrop-blur-xl"
            : "max-w-[1400px] bg-transparent"
        }`}
      >
        <div
          className={`flex items-center justify-between px-6 transition-all duration-500 lg:px-8 ${
            isScrolled ? "h-14" : "h-20"
          }`}
        >
          <Link href="/" className="group flex items-center gap-2">
            <span
              className={`font-display tracking-tight transition-all duration-500 ${
                isScrolled ? "text-xl" : "text-2xl"
              }`}
            >
              ClearGate
            </span>
            {/*
             * Stated in the chrome of every page, because it changes what every
             * amount on the site means.
             */}
            <span
              className={`font-mono tracking-widest text-muted-foreground uppercase transition-all duration-500 ${
                isScrolled ? "mt-0.5 text-[10px]" : "mt-1 text-xs"
              }`}
            >
              testnet
            </span>
          </Link>

          <div className="hidden items-center gap-12 md:flex">
            {navLinks.map((link) => (
              <Link
                key={link.name}
                href={link.href}
                className="group relative text-sm text-foreground/70 transition-colors duration-300 hover:text-foreground"
              >
                {link.name}
                <span className="absolute -bottom-1 left-0 h-px w-0 bg-accent transition-all duration-300 group-hover:w-full" />
              </Link>
            ))}
          </div>

          <div className="hidden items-center gap-4 md:flex">
            <a
              href="https://portal.hedera.com"
              className={`text-foreground/70 transition-all duration-500 hover:text-foreground ${
                isScrolled ? "text-xs" : "text-sm"
              }`}
            >
              Get testnet HBAR
            </a>
            <Button
              asChild
              size="sm"
              className={`rounded-full transition-all duration-500 ${
                isScrolled ? "h-8 px-4 text-xs" : "px-6"
              }`}
            >
              <Link href="/provide">List your GPU</Link>
            </Button>
          </div>

          <button
            onClick={() => setIsMobileMenuOpen(!isMobileMenuOpen)}
            className="p-2 md:hidden"
            aria-label="Toggle menu"
            aria-expanded={isMobileMenuOpen}
          >
            {isMobileMenuOpen ? <X className="h-6 w-6" /> : <Menu className="h-6 w-6" />}
          </button>
        </div>
      </nav>

      <div
        className={`fixed inset-0 z-40 bg-background transition-all duration-500 md:hidden ${
          isMobileMenuOpen ? "pointer-events-auto opacity-100" : "pointer-events-none opacity-0"
        }`}
        style={{ top: 0 }}
      >
        <div className="flex h-full flex-col px-8 pt-28 pb-8">
          <div className="flex flex-1 flex-col justify-center gap-8">
            {navLinks.map((link, i) => (
              <Link
                key={link.name}
                href={link.href}
                onClick={() => setIsMobileMenuOpen(false)}
                className={`font-display text-5xl text-foreground transition-all duration-500 hover:text-accent ${
                  isMobileMenuOpen ? "translate-y-0 opacity-100" : "translate-y-4 opacity-0"
                }`}
                style={{ transitionDelay: isMobileMenuOpen ? `${i * 75}ms` : "0ms" }}
              >
                {link.name}
              </Link>
            ))}
          </div>

          <div
            className={`flex gap-4 border-t border-foreground/10 pt-8 transition-all duration-500 ${
              isMobileMenuOpen ? "translate-y-0 opacity-100" : "translate-y-4 opacity-0"
            }`}
            style={{ transitionDelay: isMobileMenuOpen ? "300ms" : "0ms" }}
          >
            <Button asChild variant="outline" className="h-14 flex-1 rounded-full text-base">
              <Link href="/nodes" onClick={() => setIsMobileMenuOpen(false)}>
                Browse nodes
              </Link>
            </Button>
            <Button asChild className="h-14 flex-1 rounded-full text-base">
              <Link href="/provide" onClick={() => setIsMobileMenuOpen(false)}>
                List your GPU
              </Link>
            </Button>
          </div>
        </div>
      </div>
    </header>
  );
}
