/**
 * Builds the Content-Security-Policy. With a nonce, scripts need that nonce (or to be loaded
 * by a script that had it); without one, inline scripts are allowed because statically
 * rendered pages are built before any request exists to carry a nonce. `origin` is the
 * site the policy is served for: the public origin unless the staff host is answering.
 */
export function contentSecurityPolicy(nonce?: string, origin = process.env.DONOR_PUBLIC_ORIGIN): string {
  // React reconstructs server error stacks with eval in development only.
  const evaluation = process.env.NODE_ENV === "development" ? " 'unsafe-eval'" : "";
  // Upgrading on a plain-HTTP origin would break every subresource, so it follows the origin.
  const httpsOrigin = origin?.startsWith("https://") ?? false;
  return [
    "default-src 'self'",
    `script-src 'self' ${nonce ? `'nonce-${nonce}' 'strict-dynamic'` : "'unsafe-inline'"}${evaluation}`,
    // styled-jsx, the font loader, and style attributes are inline. A nonce here would make
    // browsers ignore 'unsafe-inline', and style attributes cannot carry one.
    "style-src 'self' 'unsafe-inline'",
    "img-src 'self' data: blob:",
    "font-src 'self' data:",
    "connect-src 'self'",
    "frame-ancestors 'none'",
    "base-uri 'self'",
    "form-action 'self'",
    "object-src 'none'",
    ...(httpsOrigin ? ["upgrade-insecure-requests"] : []),
  ].join("; ");
}
