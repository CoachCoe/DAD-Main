"use client";
import { useEffect, useRef, useState } from "react";
import { usePathname } from "next/navigation";
import Link from "next/link";

const destinations = [
  ["/initiatives", "Initiatives"],
  ["/ledger", "Impact ledger"],
  ["/how-it-works", "How it works"],
  ["/account/dashboard", "My account"],
] as const;

export function DonorHeader() {
  const pathname = usePathname();
  // Remembering where the menu was opened closes it on navigation without an effect.
  const [openOn, setOpenOn] = useState<string | null>(null);
  const open = openOn === pathname;
  const toggle = useRef<HTMLButtonElement>(null);
  useEffect(() => {
    if (!open) return;
    const close = (event: KeyboardEvent) => { if (event.key === "Escape") { setOpenOn(null); toggle.current?.focus(); } };
    document.addEventListener("keydown", close);
    return () => document.removeEventListener("keydown", close);
  }, [open]);
  return <header className="header dadHeader">
    <Link href="/" className="brand"><span className="brandMark" aria-hidden>D</span><span>Dynamic Aid Delivery</span></Link>
    <button className="burger" type="button" ref={toggle} aria-label="Menu" aria-expanded={open} aria-controls="dad-menu"
      onClick={() => setOpenOn(open ? null : pathname)}><span aria-hidden /><span aria-hidden /><span aria-hidden /></button>
    <div className="headerMenu" id="dad-menu" data-open={open}>
      <nav aria-label="Primary navigation">{destinations.map(([href, label]) =>
        <Link key={href} href={href} aria-current={pathname === href ? "page" : undefined}>{label}</Link>)}</nav>
      <div className="headerRight"><Link className="button" href="/donate" aria-current={pathname === "/donate" ? "page" : undefined}>Give</Link></div>
    </div>
  </header>;
}
