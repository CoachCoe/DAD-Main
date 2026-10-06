import type { Metadata } from "next";
import Link from "next/link";
import { Page } from "@/components/donor/Page";

export const metadata: Metadata = { title: "Page not found", robots: { index: false } };

export default function NotFound() {
  return <Page eyebrow="Not found" title="We couldn’t find this page">
    <p className="lead">The link may be out of date, or the page may not be published yet.</p>
    <div className="actions">
      <Link className="button" href="/">Back to home</Link>
      <Link className="secondary" href="/how-it-works">How DAD works</Link>
    </div>
  </Page>;
}
