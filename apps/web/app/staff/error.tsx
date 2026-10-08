"use client";

import Link from "next/link";
import { Page } from "@/components/donor/Page";

export default function StaffError({ error, retry }: { error: Error & { digest?: string }; retry: () => void }) {
  return <Page eyebrow="Something went wrong" title="This page didn’t load">
    <p className="lead">Please try again. Before repeating an action, check whether it took effect.</p>
    {error.digest && <p className="note">If this keeps happening, quote reference <span className="hash">{error.digest}</span>.</p>}
    <div className="actions">
      <button type="button" className="button" onClick={retry}>Try again</button>
      <Link className="secondary" href="/staff">Go to staff home</Link>
    </div>
  </Page>;
}
