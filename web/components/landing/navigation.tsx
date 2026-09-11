"use client";

import { useEffect, useState } from "react";
import Link from "next/link";
import { usePathname } from "next/navigation";
import { Menu, X } from "lucide-react";

import { Button } from "@/components/ui/button";

const navLinks = [
  { name: "Nodes", href: "/nodes" },
  { name: "How it works", href: "/#how-it-works" },
  { name: "Features", href: "/#features" },
  { name: "Developers", href: "/#developers" },
];

/**
 * The site chrome.
 *
 * Square, not rounded: the radius rule across the site is pills for actions and
 * square for surfaces, and a navigation bar is a surface. It was the only
 * `rounded-2xl` anywhere.
 *
 * Nothing here resizes its type on scroll. The bar used to shrink every element
 * independently, which is where seven different text sizes in one component
 * came from — the height and the rules move, the type does not.
 */
export function Navigation() {
  const pathname = usePathname();
  /** A section link is never "current"; only a real route is. */
  const active = (href: string) => !href.includes("#") && pathname === href;

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

  const docked = isScrolled || isMobileMenuOpen;

  return (
    <header className="fixed inset-x-0 top-0 z-50">
      <div
        className={`transition-[background-color,border-color] ease-brand dur-slow ${
          docked
            ? "border-b border-foreground/10 bg-background/80 backdrop-blur-xl"
            : "border-b border-transparent bg-transparent"
        }`}
      >
        <div
          className={`mx-auto flex max-w-[1400px] items-center justify-between px-6 transition-[height] ease-brand dur-slow lg:px-12 ${
            docked ? "h-16" : "h-20"
          }`}
        >
          <Link href="/" className="group flex items-baseline gap-2">
            <span className="type-wordmark">ClearGate</span>
            {/*
             * Stated in the chrome of every page, because it changes what every
             * amount on the site means.
             */}
            <span className="type-label text-muted-foreground transition-colors ease-brand dur-base group-hover:text-accent">
              testnet
            </span>
          </Link>

          <div className="hidden items-center gap-5 md:flex">
            <a
              href="https://portal.hedera.com"
              className="type-label text-muted-foreground transition-colors ease-brand dur-base hover:text-foreground"
            >
              Get testnet HBAR
            </a>

            {/*
             * Links and the call to action share one bounded panel, with the
             * action filling its right-hand end. Grouping them says they are
             * the same control surface; the hairline gives the bar an edge to
             * sit against on a page that is otherwise unlit.
             */}
            <nav className="flex items-stretch border border-foreground/20 bg-foreground/[0.06] backdrop-blur-md">
              {navLinks.map((link) => (
                <Link
                  key={link.name}
                  href={link.href}
                  aria-current={active(link.href) ? "page" : undefined}
                  className={`type-nav group relative flex items-center px-5 transition-colors ease-brand dur-base hover:text-foreground ${
                    active(link.href) ? "text-foreground" : "text-foreground/70"
                  }`}
                >
                  {link.name}
                  <span
                    className={`absolute inset-x-3 bottom-2 h-px bg-accent transition-transform ease-brand dur-base group-hover:scale-x-100 ${
                      active(link.href) ? "scale-x-100" : "scale-x-0"
                    }`}
                  />
                </Link>
              ))}

              <Button asChild shape="square" className="type-nav h-11 px-5">
                <Link href="/provide">List your GPU</Link>
              </Button>
            </nav>
          </div>

          <Button
            variant="quiet"
            size="icon"
            shape="square"
            onClick={() => setIsMobileMenuOpen(!isMobileMenuOpen)}
            aria-label="Toggle menu"
            aria-expanded={isMobileMenuOpen}
            className="md:hidden"
          >
            {isMobileMenuOpen ? <X className="size-5" /> : <Menu className="size-5" />}
          </Button>
        </div>
      </div>

      <div
        className={`fixed inset-0 z-40 bg-background transition-opacity ease-brand dur-slow md:hidden ${
          isMobileMenuOpen ? "pointer-events-auto opacity-100" : "pointer-events-none opacity-0"
        }`}
      >
        <div className="flex h-full flex-col px-6 pt-24 pb-8">
          <div className="flex flex-1 flex-col justify-center gap-6">
            {navLinks.map((link, i) => (
              <Link
                key={link.name}
                href={link.href}
                onClick={() => setIsMobileMenuOpen(false)}
                className={`type-title text-foreground transition-all ease-brand dur-slow hover:text-accent ${
                  isMobileMenuOpen ? "translate-y-0 opacity-100" : "translate-y-4 opacity-0"
                }`}
                style={{ transitionDelay: isMobileMenuOpen ? `${i * 60}ms` : "0ms" }}
              >
                {link.name}
              </Link>
            ))}
          </div>

          <div
            className={`flex gap-3 border-t border-foreground/10 pt-8 transition-all ease-brand dur-slow ${
              isMobileMenuOpen ? "translate-y-0 opacity-100" : "translate-y-4 opacity-0"
            }`}
            style={{ transitionDelay: isMobileMenuOpen ? "260ms" : "0ms" }}
          >
            <Button asChild variant="outline" size="lg" className="flex-1">
              <Link href="/nodes" onClick={() => setIsMobileMenuOpen(false)}>
                Browse nodes
              </Link>
            </Button>
            <Button asChild size="lg" className="flex-1">
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
