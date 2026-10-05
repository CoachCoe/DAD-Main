import Link from "next/link";
import { DonorHeader } from "./DonorHeader";

export function SiteFrame({ children }: { children: React.ReactNode }) {
  return <><a className="skipLink" href="#main">Skip to content</a><DonorHeader />
    <main id="main" className="donorSurface">{children}</main>
    <footer>Dynamic Aid Delivery · Donor release preview<span>DAD launches in April 2027. Giving in this environment uses Stripe test payments.</span>
      <span><Link href="/how-it-works">About DAD</Link> · <Link href="/partners">Partner interest</Link> · <Link href="/legal">Terms, privacy, and donor consent</Link></span></footer>
  </>;
}
