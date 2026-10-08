"use client";
import { usePathname } from "next/navigation";
import Link from "next/link";

export function SiteFooter() {
  // The back office has its own host, which serves none of these public pages.
  if (usePathname().startsWith("/staff")) return null;
  return <footer>
    <p>Dynamic Aid Delivery · Donor preview. DAD launches in April 2027; giving here uses Stripe test payments.</p>
    <nav aria-label="Footer">
      <Link href="/how-it-works">About DAD</Link>
      <Link href="/partners">Partner interest</Link>
      <Link href="/legal">Terms, privacy, and donor consent</Link>
    </nav>
  </footer>;
}
