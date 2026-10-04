import Link from "next/link";
import type { Metadata } from "next";

export const metadata: Metadata = {
  title: "Page not found — Dynamic Aid Delivery",
  robots: { index: false },
};

export default function NotFound() {
  return (
    <div className="workspace">
      <section className="panel">
        <span className="eyebrow">NOT FOUND</span>
        <h1>We couldn’t find this page</h1>
        <p className="subtle">
          The link may be out of date or the page may not be published yet.
          You can return home or learn how DAD works.
        </p>
        <div className="actionRow">
          <Link className="button" href="/">
            Back to home
          </Link>
          <Link className="secondary" href="/about">
            How this works
          </Link>
        </div>
      </section>
    </div>
  );
}
