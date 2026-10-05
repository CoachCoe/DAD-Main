import { expect, test, type Page } from "@playwright/test";

// Init scripts run outside the page's policy, so the listener sees every violation.
async function recordViolations(page: Page) {
  const violations: string[] = [];
  page.on("console", (message) => {
    if (/Content Security Policy|Refused to (execute|load|apply)/i.test(message.text())) violations.push(message.text());
  });
  await page.addInitScript(() => {
    document.addEventListener("securitypolicyviolation", (event) => {
      console.error(`Refused to execute (securitypolicyviolation): ${event.violatedDirective} ${event.blockedURI}`);
    });
  });
  return violations;
}

const hydrated = (page: Page, selector: string) =>
  page.locator(selector).first().evaluate((element) => Object.keys(element).some((key) => key.startsWith("__reactFiber")));

test("the donor dashboard runs under a per-request nonce policy", async ({ page }) => {
  const violations = await recordViolations(page);
  const response = await page.goto("/account/dashboard");
  const policy = response?.headers()["content-security-policy"] ?? "";
  expect(policy).toMatch(/script-src 'self' 'nonce-[^']+' 'strict-dynamic'/);
  expect(policy).not.toMatch(/script-src[^;]*'unsafe-inline'/);
  // Only rendered after the client has asked the API for a session.
  await expect(page.getByRole("link", { name: "Sign in to view your dashboard" })).toBeVisible();
  expect(violations).toEqual([]);

  const again = await page.reload();
  expect(again?.headers()["content-security-policy"]).not.toBe(policy);
});

test("staff sign-in hydrates under the nonce policy", async ({ page }) => {
  const violations = await recordViolations(page);
  const response = await page.goto("/staff/sign-in");
  expect(response?.headers()["content-security-policy"]).toMatch(/'nonce-[^']+' 'strict-dynamic'/);
  await expect(page.getByRole("heading", { level: 1 })).toBeVisible();
  await expect.poll(() => hydrated(page, "form")).toBe(true);
  expect(violations).toEqual([]);
});

test("public pages keep the static, cacheable policy", async ({ page }) => {
  const violations = await recordViolations(page);
  const response = await page.goto("/");
  const policy = response?.headers()["content-security-policy"] ?? "";
  expect(policy).toMatch(/script-src 'self' 'unsafe-inline'/);
  expect(policy).not.toContain("nonce-");
  await expect.poll(() => hydrated(page, "main")).toBe(true);
  expect(violations).toEqual([]);
});
