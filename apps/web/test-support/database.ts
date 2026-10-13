/**
 * Environment giving a process the test database's URL acting as its database role, so
 * the process under test holds only that role's privileges. The test user, a superuser,
 * assumes the role, which a process accepts only where owner connections are allowed.
 * Browser journeys may provision staff, so an application database must never be used.
 */
export function databaseAs(role: string): Record<string, string> {
  const owner = process.env.DONOR_GO_DATABASE_URL;
  let url: URL;
  try {
    url = new URL(owner ?? "");
  } catch {
    throw new Error("Browser tests require DONOR_GO_DATABASE_URL pointing to a dedicated donor_go database.");
  }
  if (!["postgres:", "postgresql:"].includes(url.protocol) || url.pathname !== "/donor_go") {
    throw new Error("Browser tests refuse databases other than the dedicated donor_go test database.");
  }
  url.searchParams.set("role", role);
  return {
    [`DONOR_${role.replace(/^dad_/, "").toUpperCase()}_DATABASE_URL`]: url.href,
    DONOR_ALLOW_OWNER_CONNECTION: "true",
  };
}
