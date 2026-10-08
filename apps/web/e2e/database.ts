/**
 * Environment giving a process the test database's URL acting as its database role, so
 * the process under test holds only that role's privileges. The test user, a superuser,
 * assumes the role, which a process accepts only where owner connections are allowed.
 * Without DONOR_GO_DATABASE_URL there is no database to give.
 */
export function databaseAs(role: string): Record<string, string> {
  const owner = process.env.DONOR_GO_DATABASE_URL;
  if (!owner) return {};
  const url = new URL(owner);
  url.searchParams.set("role", role);
  return {
    [`DONOR_${role.replace(/^dad_/, "").toUpperCase()}_DATABASE_URL`]: url.href,
    DONOR_ALLOW_OWNER_CONNECTION: "true",
  };
}
