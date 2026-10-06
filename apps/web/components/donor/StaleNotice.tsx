import { formatDateTime } from "@/lib/dates";

/** Shown when the API could not reach Stripe and answered with its last stored state. */
export function StaleNotice({ observedAt }: { observedAt?: string }) {
  return <p className="callout warn" role="note">
    Showing the last known details{observedAt && <> as of <time dateTime={observedAt}>{formatDateTime(observedAt)}</time></>}; changes are
    unavailable until Stripe responds. Please refresh in a few minutes.
  </p>;
}
