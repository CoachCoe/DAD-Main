import { isIP } from "node:net";

/**
 * The visitor's address as reported by trusted ingress, for the API's per-client rate
 * limits. Enable DONOR_CLIENT_IP_HEADER only behind ingress that overwrites that header
 * and blocks direct access; otherwise a visitor could choose their own address.
 */
export function trustedClientIp(headers: Headers): string | null {
  const name = process.env.DONOR_CLIENT_IP_HEADER;
  const value = name ? headers.get(name) : null;
  return value && isIP(value) ? value : null;
}
