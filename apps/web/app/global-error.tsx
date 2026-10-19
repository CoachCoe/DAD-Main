"use client";

import "./globals.css";
import { webTokenVariables } from "@/design/web";

// Replaces the root layout, so it carries its own document, tokens and title.
export default function GlobalError({ error, retry }: { error: Error & { digest?: string }; retry: () => void }) {
  return (
    <html lang="en" style={webTokenVariables}>
      <body>
        <title>Something went wrong · Dynamic Aid Delivery</title>
        <main id="main">
          <div className="page narrow">
            <section className="panel">
              <span className="eyebrow">Something went wrong</span>
              <h1>Dynamic Aid Delivery didn’t load</h1>
              <p className="lead">
                Please try again. After a checkout problem, check your giving history before starting another gift.
              </p>
              {error.digest && (
                <p className="note">
                  If this keeps happening, quote reference <span className="hash">{error.digest}</span> to DAD.
                </p>
              )}
              <div className="actions">
                <button type="button" className="button" onClick={retry}>
                  Try again
                </button>
                {/* A full page load: the error may have left client navigation unusable. */}
                <a className="secondary" href="/">
                  Back to home
                </a>
              </div>
            </section>
          </div>
        </main>
      </body>
    </html>
  );
}
