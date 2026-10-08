"use client";

import Link from "next/link";
import { Page } from "@/components/donor/Page";

export default function PageError({ error, retry }: { error: Error & { digest?: string }; retry: () => void }) {
  return <Page eyebrow="Something went wrong" title="This page didn’t load">
    <p className="lead">Please try again. After a checkout problem, check your giving history before starting another gift.</p>
    {error.digest && <p className="note">If this keeps happening, quote reference <span className="hash">{error.digest}</span> to DAD.</p>}
    <div className="actions">
      <button type="button" className="button" onClick={retry}>Try again</button>
      <Link className="secondary" href="/">Back to home</Link>
    </div>
  </Page>;
}
