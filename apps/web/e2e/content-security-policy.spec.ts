import { expect, test, type Page } from "@playwright/test";
import { staffOrigin, staffURL } from "./staff";

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

test("prefetch headers cannot remove a private document's security policy", async ({ request }) => {
  // The staff host is named by Host rather than resolved, so the runner's resolver cannot matter.
  for (const [path, host] of [["/account/sign-in", ""], ["/staff/sign-in", new URL(staffOrigin).host]]) {
    for (const [name, value] of [["next-router-prefetch", "1"], ["purpose", "prefetch"]]) {
      const response = await request.get(path, { headers: { ...(host ? { host } : {}), [name]: value } });
      expect(response.status()).toBe(200);
      expect(response.headers()["content-security-policy"]).toMatch(/'nonce-[^']+' 'strict-dynamic'/);
    }
  }
});

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
  const response = await page.goto(staffURL("/staff/sign-in"));
  expect(response?.headers()["content-security-policy"]).toMatch(/'nonce-[^']+' 'strict-dynamic'/);
  await expect(page.getByRole("heading", { level: 1 })).toBeVisible();
  await expect.poll(() => hydrated(page, "main button")).toBe(true);
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

test("public account links load a new document with the private CSP", async ({ page }) => {
  const violations = await recordViolations(page);
  for (const [source, label, destination] of [
    ["/", "My account", "/account/dashboard"],
    ["/donate/thanks", "Claim your account", "/account/sign-in"],
  ]) {
    await page.goto(source);
    await expect.poll(() => hydrated(page, "main")).toBe(true);
    if (label === "My account" && await page.getByRole("button", { name: "Menu", exact: true }).isVisible()) {
      await page.getByRole("button", { name: "Menu", exact: true }).click();
    }
    const documentResponse = page.waitForResponse((response) =>
      new URL(response.url()).pathname === destination && response.request().resourceType() === "document");
    await page.getByRole("link", { name: label, exact: true }).click();
    const response = await documentResponse;
    expect(response.headers()["content-security-policy"]).toMatch(/'nonce-[^']+' 'strict-dynamic'/);
    expect(response.headers()["content-security-policy"]).not.toMatch(/script-src[^;]*'unsafe-inline'/);
    await expect.poll(() => hydrated(page, "main")).toBe(true);
  }
  expect(violations).toEqual([]);
});
