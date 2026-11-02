import Link from "next/link";
/** The one place that says the donor site is a test environment; other pages stay quiet. */
export function PreviewBanner() {
  return (
    <aside className="previewBanner" aria-label="Preview notice">
      <span className="previewLabel">Preview</span>
      <p>
        Test payments only. <span className="previewMore">No real money moves.</span>
      </p>
      <Link href="/legal">Details</Link>
    </aside>
  );
}
