"use client";

import Link from "next/link";

export default function PageError({
  error,
  reset,
}: {
  error: Error & { digest?: string };
  reset: () => void;
}) {
  return (
    <div className="workspace">
      <section className="panel">
        <span className="eyebrow">SOMETHING BROKE</span>
        <h1>This page did not load</h1>
        <p className="subtle">
          Please try again. If this keeps happening, the reference below can help DAD
          investigate. After a checkout problem, check your giving history before starting another gift.
        </p>
        {error.digest ? (
          <p className="note">
            Reference <span className="hash">{error.digest}</span>
          </p>
        ) : null}
        <div className="actionRow">
          <button type="button" className="button" onClick={reset}>
            Try again
          </button>
          <Link className="secondary" href="/">
            Back to home
          </Link>
        </div>
      </section>
    </div>
  );
}
