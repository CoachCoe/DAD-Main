import Link from "next/link";

export function SiteFooter() {
  return (
    <footer>
      <p>Dynamic Aid Delivery · Donor preview. DAD launches in April 2027; giving here uses Stripe test payments.</p>
      <nav aria-label="Footer">
        <Link href="/how-it-works">About DAD</Link>
        <Link href="/partners">Partner interest</Link>
        <Link href="/legal">Terms, privacy, and donor consent</Link>
      </nav>
    </footer>
  );
}
