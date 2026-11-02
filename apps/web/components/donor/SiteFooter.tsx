import Link from "next/link";

export function SiteFooter() {
  return (
    <footer>
      <p className="tagline">Aid, delivered.</p>
      <nav aria-label="Footer">
        <Link href="/how-it-works">About DAD</Link>
        <Link href="/apply">Apply as a partner</Link>
        <Link href="/legal">Terms, privacy, and donor consent</Link>
      </nav>
    </footer>
  );
}
