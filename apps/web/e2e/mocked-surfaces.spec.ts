import AxeBuilder from "@axe-core/playwright";
import { expect, test, type Page } from "@playwright/test";

// These isolate rendering of states that need a session or a live referral code; Go
// journey tests exercise the real authorization and attribution behind them.
async function expectAccessible(page: Page) {
  const results = await new AxeBuilder({ page }).withTags(["wcag2a", "wcag2aa", "wcag21aa", "wcag22aa"]).analyze();
  expect(results.violations).toEqual([]);
  expect(await page.locator("body").evaluate((body) => body.scrollWidth <= innerWidth + 1)).toBe(true);
}

function collectErrors(page: Page) {
  const errors: string[] = [];
  page.on("pageerror", (error) => errors.push(error.message));
  return errors;
}

// Development StrictMode replays effects, so a dev server may send the capture twice;
// every request must still carry the same kind.
const kinds = (captures: unknown[]) => [...new Set(captures.map((capture) => JSON.stringify(capture)))];

const code = `<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 10 10"><rect width="10" height="10" fill="#000"/></svg>`;

test("referral landing is accessible and captures an IRL visit from the browser", async ({ page }) => {
  const errors = collectErrors(page);
  const captures: unknown[] = [];
  await page.route("**/api/donor/referrals/*", async (route) => {
    expect(route.request().method()).toBe("POST");
    captures.push(route.request().postDataJSON());
    await route.fulfill({ json: { captured: true, public_name: null } });
  });
  await page.goto("/r/e2e-browser-code?via=irl");
  await expect(page.getByRole("heading", { level: 1 })).toBeVisible();
  await expect(page.getByRole("main").getByRole("status")).toBeEmpty();
  expect(kinds(captures)).toEqual([JSON.stringify({ kind: "IRL" })]);
  await expect(page.getByText("About a dollar a day · $30/month.")).toBeVisible();
  await expectAccessible(page);
  expect(errors).toEqual([]);
});

test("a shared link without the marker is captured as a Link Referral", async ({ page }) => {
  const captures: unknown[] = [];
  await page.route("**/api/donor/referrals/*", async (route) => {
    captures.push(route.request().postDataJSON());
    await route.fulfill({ json: { captured: true, public_name: null } });
  });
  await page.goto("/r/e2e-browser-code");
  await expect(page.getByRole("main").getByRole("status")).toBeEmpty();
  expect(kinds(captures)).toEqual([JSON.stringify({ kind: "LINK" })]);
});

test("a rate-limited referral capture explains the wait and recovers", async ({ page }) => {
  let limited = true;
  await page.route("**/api/donor/referrals/*", (route) => limited
    ? route.fulfill({ status: 429, json: { detail: "Too many requests" } })
    : route.fulfill({ json: { captured: true, public_name: null } }));
  await page.goto("/r/e2e-browser-code");
  await expect(page.getByRole("main").getByRole("alert")).toContainText("Wait a minute");
  await expectAccessible(page);
  limited = false;
  await page.getByRole("button", { name: "Retry referral" }).click();
  await expect(page.getByRole("main").getByRole("alert")).toBeEmpty();
});

test("the referral code page is accessible and shows the code", async ({ page }) => {
  const errors = collectErrors(page);
  await page.route("**/api/donor/me/sharing", (route) => route.fulfill({ json: { public_name: "Alex", link: "https://dad.example/r/x", irl_link: "https://dad.example/r/x?via=irl" } }));
  await page.route("**/api/donor/me/referral-code.svg*", (route) => route.fulfill({ contentType: "image/svg+xml", body: code }));
  await page.route("**/api/donor/me/wallet", (route) => route.fulfill({ json: { apple: false, google: false } }));
  await page.goto("/account/code");
  await expect(page.getByRole("img", { name: "Your scannable in-person DAD referral code" })).toBeVisible();
  await expect(page.getByText(/Increase your screen brightness/)).toBeVisible();
  await expectAccessible(page);
  expect(errors).toEqual([]);
});

test("a staff page past sign-in is accessible", async ({ page }) => {
  const errors = collectErrors(page);
  await page.route("**/api/donor/staff/me", (route) => route.fulfill({ json: {
    id: "staff-1", email: "admin@example.org", name: "Ada Admin", role: "ADMIN", active: true, disabled: false,
    permissions: ["data.export", "content.publish", "audit.read", "gift.refund", "gift.cancel", "staff.manage"],
  } }));
  await page.goto("/staff");
  await expect(page.getByText("Ada Admin · ADMIN")).toBeVisible();
  await expect(page.getByRole("button", { name: "Search donors" })).toBeVisible();
  await expectAccessible(page);
  expect(errors).toEqual([]);
});

test("staff pages separate a missing session from a missing permission", async ({ page }) => {
  await page.route("**/api/donor/staff/audit*", (route) => route.fulfill({ status: 403, json: { detail: "Forbidden" } }));
  await page.goto("/staff/audit");
  await expect(page.getByRole("main").getByRole("alert")).toContainText("requires administrator access");
  await expect(page.getByRole("link", { name: "Staff sign-in" })).toHaveCount(0);
  await page.unroute("**/api/donor/staff/audit*");
  await page.route("**/api/donor/staff/audit*", (route) => route.fulfill({ status: 401, json: { detail: "Sign in" } }));
  await page.reload();
  await expect(page.getByRole("link", { name: "Staff sign-in" })).toBeVisible();
});

test("public pages carry their own titles and private pages are not indexed", async ({ page }) => {
  await page.goto("/ledger");
  await expect(page).toHaveTitle("Impact ledger · Dynamic Aid Delivery");
  await page.goto("/account/sign-in");
  await expect(page).toHaveTitle(/^Claim your account or sign in · /);
  await expect(page.locator('meta[name="robots"]')).toHaveAttribute("content", /noindex/);
  await page.goto("/staff/sign-in");
  await expect(page.locator('meta[name="robots"]')).toHaveAttribute("content", /noindex/);
});
