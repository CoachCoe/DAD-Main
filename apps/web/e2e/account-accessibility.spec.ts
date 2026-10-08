import AxeBuilder from "@axe-core/playwright";
import { expect, test } from "@playwright/test";

const dashboard = {
  mode: "test", state: "ready", computed_at: "2026-10-04T12:00:00Z", founding_donor: true,
  referrals: { link: 12, irl: 7, total_minor: 57000, link_minor: 36000, irl_minor: 21000, currency: "USD" },
  streak: { current: 3, at_risk: false, grace_ends_at: null },
};
const profiles = {
  "/account/dashboard": { endpoint: "dashboard", data: dashboard },
  "/account/profile": { endpoint: "profile", data: { email: "browser-test@example.org", public_name: "Alex", version: 1, referral_emails: true, marketing_emails: false } },
  "/account/share": { endpoint: "sharing", data: { public_name: "Alex", link: "https://dad.example/r/browser-test", irl_link: "https://dad.example/r/browser-test?via=irl" } },
};

// These isolate authenticated rendering; Go journey tests exercise real session authorization.
for (const [path, fixture] of Object.entries(profiles)) {
  test(`authenticated accessible surface: ${path}`, async ({ page }) => {
    const errors: string[] = [];
    page.on("pageerror", (error) => errors.push(error.message));
    await page.route(`**/api/donor/me/${fixture.endpoint}`, (route) => route.fulfill({ json: fixture.data }));
    await page.goto(path);
    await expect(page.getByRole("heading", { level: 1 })).toBeVisible();
    await expect(page.getByText(/Loading (your dashboard|profile|your sharing details)/)).toHaveCount(0);
    if (path === "/account/dashboard" && (page.viewportSize()?.width ?? 0) > 600) {
      const link = await page.getByRole("article").filter({ has: page.getByRole("heading", { name: "Link Referrals", exact: true }) }).boundingBox();
      const irl = await page.getByRole("article").filter({ has: page.getByRole("heading", { name: "IRL Referrals", exact: true }) }).boundingBox();
      expect(link).not.toBeNull(); expect(irl).not.toBeNull();
      expect(link?.y).toBe(irl?.y);
      expect(link?.width).toBe(irl?.width);
    }
    if (path === "/account/profile") {
      await page.getByText("Change account email", { exact: true }).click();
      await expect(page.getByLabel("New email address")).toBeVisible();
    }
    const results = await new AxeBuilder({ page }).withTags(["wcag2a", "wcag2aa", "wcag21aa", "wcag22aa"]).analyze();
    expect(results.violations).toEqual([]);
    expect(errors).toEqual([]);
    expect(await page.locator("body").evaluate((body) => body.scrollWidth <= innerWidth + 1)).toBe(true);
  });
}

const subscription = { id: "6f1c2b9e-0d7a-4e55-9b0c-1a2b3c4d5e6f", amount_minor: 3000, currency: "USD", status: "active",
  period_end: "2026-11-01T14:00:00Z", pause: null, stops_at: null };
const recurring = {
  active: { subscription, text: /^Next renewal: Nov 1, 2026\.$/, links: ["Update payment method", "Pause monthly giving", "Change monthly amount"] },
  paused: { subscription: { ...subscription, pause: { starts_at: "2020-11-01T14:00:00Z", resumes_at: "2027-01-01T14:00:00Z" } },
    text: /^Your monthly gift is paused\. Giving resumes Jan 1, 2027\.$/, links: ["Update payment method", "Resume at next renewal"] },
  "scheduled-pause": { subscription: { ...subscription, pause: { starts_at: "2099-11-01T14:00:00Z", resumes_at: "2100-01-01T14:00:00Z" } },
    text: /^Your monthly gift pauses from Nov 1, 2099\. Giving resumes Jan 1, 2100\.$/, links: ["Update payment method", "Resume at next renewal"] },
  ending: { subscription: { ...subscription, stops_at: subscription.period_end },
    text: /^Renewals cancelled\. Your last monthly gift period ends Nov 1, 2026; no further gifts will be charged\.$/, links: ["Update payment method"] },
};

for (const [name, fixture] of Object.entries(recurring)) {
  test(`monthly giving describes a ${name} gift accessibly`, async ({ page }) => {
    await page.route("**/api/donor/me/recurring", (route) => route.fulfill({ json: {
      mode: "test", state: "ready", observed_at: "2026-10-07T13:42:00Z", subscription: fixture.subscription } }));
    await page.goto("/account/recurring");
    await expect(page.getByText(fixture.text)).toBeVisible();
    await expect(page.getByText(/^Billing details last updated from Stripe /)).toBeVisible();
    const nav = page.getByRole("navigation", { name: "Manage monthly giving" });
    await expect(nav.getByRole("link")).toHaveText(fixture.links);
    const cancel = page.getByRole("button", { name: "Cancel monthly giving" });
    if (name === "ending") await expect(cancel).toHaveCount(0);
    else {
      const linkBox = await nav.getByRole("link").first().boundingBox();
      const textBox = await cancel.evaluate((button) => {
        const range = document.createRange();
        range.selectNodeContents(button);
        return range.getBoundingClientRect().left;
      });
      expect(Math.abs((linkBox?.x ?? 0) - textBox)).toBeLessThan(1);
    }
    const results = await new AxeBuilder({ page }).withTags(["wcag2a", "wcag2aa", "wcag21aa", "wcag22aa"]).analyze();
    expect(results.violations).toEqual([]);
  });
}

test("a new monthly gift shows its confirmation first and the sandbox note last", async ({ page }) => {
  await page.route("**/api/donor/me/recurring", (route) => route.fulfill({ json: { mode: "test", state: "preparing", subscription: null } }));
  await page.goto("/account/recurring");
  const callout = page.locator(".callout").filter({ hasText: "We’re confirming your new monthly gift with Stripe." });
  await expect(callout).toBeVisible();
  await expect(callout.getByRole("button", { name: "Refresh billing details" })).toBeVisible();
  await expect(page.getByRole("main").getByRole("status")).toHaveText("We’re confirming your new monthly gift with Stripe. This usually takes a few seconds.");
  const order = await page.locator(".callout, .note").evaluateAll((nodes) => nodes.map((node) => node.className));
  expect(order).toEqual(["callout", "note"]);
  const results = await new AxeBuilder({ page }).withTags(["wcag2a", "wcag2aa", "wcag21aa", "wcag22aa"]).analyze();
  expect(results.violations).toEqual([]);
});

test("dashboard explains partial data and recovers from a connection failure", async ({ page }) => {
  let available = false;
  await page.route("**/api/donor/me/dashboard", (route) => available
    ? route.fulfill({ json: { ...dashboard, referrals: null, streak: null } })
    : route.abort("internetdisconnected"));
  await page.goto("/account/dashboard");
  await expect(page.getByRole("main").getByRole("alert")).toContainText("Check your connection");
  available = true;
  await page.getByRole("button", { name: "Refresh dashboard" }).click();
  await expect(page.getByRole("main").getByRole("alert")).toBeEmpty();
  await expect(page.getByText("Referral totals are updating. Please refresh shortly.")).toBeVisible();
  await expect(page.getByText("Your streak is temporarily unavailable.", { exact: false })).toBeVisible();
  await page.getByRole("link", { name: "Share and invite", exact: true }).focus();
  await expect(page.getByRole("link", { name: "Share and invite", exact: true })).toBeFocused();
});

test("email proof page requires an explicit keyboard confirmation", async ({ page }) => {
  const proof = "a".repeat(43);
  let confirmed = 0;
  await page.route("**/api/donor/email-change/confirm", async (route) => {
    expect(route.request().postDataJSON()).toEqual({ token: proof });
    confirmed += 1;
    await route.fulfill({ json: { stage: "COMPLETED" } });
  });
  await page.goto(`/account/email-change#token=${proof}`);
  await expect(page).toHaveURL(/\/account\/email-change$/);
  expect(confirmed).toBe(0);
  const confirm = page.getByRole("button", { name: /confirm/i });
  await confirm.focus();
  await page.keyboard.press("Enter");
  await expect(page.getByRole("link", { name: /sign in/i })).toBeVisible();
  expect(confirmed).toBe(1);
  expect((await new AxeBuilder({ page }).withTags(["wcag2a", "wcag2aa", "wcag21aa", "wcag22aa"]).analyze()).violations).toEqual([]);
});
