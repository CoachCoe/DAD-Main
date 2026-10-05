"use client";

import "./globals.css";
import { webTokenVariables } from "@/design/web";

// Replaces the root layout, so it carries its own document, tokens and title.
export default function GlobalError({ error, retry }: { error: Error & { digest?: string }; retry: () => void }) {
  return (
    <html lang="en" style={webTokenVariables}>
      <body>
        <title>Something went wrong · Dynamic Aid Delivery</title>
        <main id="main" className="donorSurface">
          <div className="workspace">
            <section className="panel">
              <span className="eyebrow">SOMETHING BROKE</span>
              <h1>Dynamic Aid Delivery did not load</h1>
              <p className="subtle">
                Please try again. If this keeps happening, the reference below can help DAD investigate.
                After a checkout problem, check your giving history before starting another gift.
              </p>
              {error.digest ? <p className="note">Reference <span className="hash">{error.digest}</span></p> : null}
              <div className="actionRow">
                <button type="button" className="button" onClick={retry}>Try again</button>
                {/* A full page load: the error may have left client navigation unusable. */}
                <a className="secondary" href="/">Back to home</a>
              </div>
            </section>
          </div>
        </main>
      </body>
    </html>
  );
}
