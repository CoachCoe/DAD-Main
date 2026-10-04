"use client";
import { useEffect, useRef, useState } from "react";
import { usePathname } from "next/navigation";
import Link from "next/link";
import { Header } from "@/components/Header";
import { SessionProvider } from "@/components/SessionProvider";

export function isDonorRoute(path: string) {
  return path === "/" || ["/account", "/donate", "/r", "/staff", "/initiatives", "/ledger", "/how-it-works", "/partners", "/legal"]
    .some((prefix) => path === prefix || path.startsWith(prefix + "/"));
}

export function DonorHeader() {
  const path = usePathname();
  const [open, setOpen] = useState(false);
  const toggle = useRef<HTMLButtonElement>(null);
  useEffect(() => setOpen(false), [path]);
  useEffect(() => {
    if (!open) return;
    const close = (event: KeyboardEvent) => { if (event.key === "Escape") { setOpen(false); toggle.current?.focus(); } };
    document.addEventListener("keydown", close);
    return () => document.removeEventListener("keydown", close);
  }, [open]);
  return <header className="header dadHeader">
    <Link href="/" className="brand"><span className="brandMark" aria-hidden>D</span><span>Dynamic Aid Delivery</span></Link>
    <button className="burger" type="button" ref={toggle} aria-label="Menu" aria-expanded={open} aria-controls="dad-menu" onClick={() => setOpen((value) => !value)}><span aria-hidden /><span aria-hidden /><span aria-hidden /></button>
    <div className="headerMenu" id="dad-menu" data-open={open}>
      <nav aria-label="Primary navigation"><Link href="/initiatives">Initiatives</Link><Link href="/ledger">Impact ledger</Link><Link href="/how-it-works">How it works</Link><Link href="/account/dashboard">My account</Link></nav>
      <div className="headerRight"><Link className="button" href="/donate">Give</Link></div>
    </div>
  </header>;
}

export function SiteFrame({ children }: { children: React.ReactNode }) {
  const donor = isDonorRoute(usePathname());
  const content = <><a className="skipLink" href="#main">Skip to content</a>{donor ? <DonorHeader /> : <Header />}
    <main id="main" className={donor ? "donorSurface" : undefined}>{children}</main>
    <footer>{donor ? <>Dynamic Aid Delivery · Donor release preview<span>DAD launches in April 2027. Giving in this environment uses Stripe test payments.</span>
      <span><Link href="/partners">Partner interest</Link> · <Link href="/legal">Terms, privacy, and donor consent</Link></span></> :
      <>ImpactGraph · Transparent provenance for real-world impact<span>This proof of concept uses demonstration data.</span></>}</footer>
  </>;
  return donor ? content : <SessionProvider>{content}</SessionProvider>;
}
