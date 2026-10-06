import Link from "next/link";
import { DonorHeader } from "./DonorHeader";

export function SiteFrame({ children }: { children: React.ReactNode }) {
  return <>
    <a className="skipLink" href="#main">Skip to content</a>
    <DonorHeader />
    <main id="main">{children}</main>
    <footer>
      <p>Dynamic Aid Delivery · Donor preview. DAD launches in April 2027; giving here uses Stripe test payments.</p>
      <nav aria-label="Footer">
        <Link href="/how-it-works">About DAD</Link>
        <Link href="/partners">Partner interest</Link>
        <Link href="/legal">Terms, privacy, and donor consent</Link>
      </nav>
    </footer>
  </>;
}
