/**
 * Set by proxy.ts on a page request the requested host does not serve, naming the host
 * ("public" or "staff") whose not-found page next.config.ts rewrites it to.
 */
export const UNSERVED_HEADER = "x-dad-unserved";

const hostname = (url?: string | null) => {
  try { return url ? new URL(url).hostname.toLowerCase() : ""; } catch { return ""; }
};

/** Whether a Host header names the staff host, DONOR_STAFF_ORIGIN, which is fixed at build time. */
export function isStaffHost(host: string | null) {
  const staff = hostname(process.env.DONOR_STAFF_ORIGIN);
  return staff !== "" && hostname(`http://${host ?? ""}`) === staff;
}
