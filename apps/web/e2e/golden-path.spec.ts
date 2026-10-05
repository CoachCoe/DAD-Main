import { expect, test, type Page } from "@playwright/test";

async function navigateByHeader(page: Page, name: string) {
  const menu = page.getByRole("button", { name: "Menu" });
  if (await menu.isVisible()) await menu.click();
  await page.getByRole("navigation", { name: "Primary navigation" }).getByRole("link", { name, exact: true }).click();
}

async function expectNoHorizontalScroll(page: Page) {
  expect(await page.locator("body").evaluate((body) => body.scrollWidth <= window.innerWidth + 1)).toBe(true);
}

test("a mobile or desktop visitor can reach giving without an account", async ({ page }) => {
  await page.goto("/");
  await expect(page.getByRole("heading", { level: 1 })).toContainText("Grow your circle");
  await expect(page.getByText(/DAD launches on 22 April 2027/)).toBeVisible();
  await expect(page.getByRole("complementary", { name: "DAD at a glance" })).toContainText("$30/month");
  await page.getByRole("link", { name: "Try giving", exact: true }).click();
  await expect(page).toHaveURL(/\/donate$/);
  await expect(page.getByLabel("Email")).toBeVisible();
  await expect(page.getByText(/\$30\/month/).first()).toBeVisible();
  await expectNoHorizontalScroll(page);
});

test("every public header destination works at this viewport", async ({ page }) => {
  await page.goto("/");
  for (const [name, path] of [["Initiatives", "/initiatives"], ["Impact ledger", "/ledger"], ["How it works", "/how-it-works"], ["My account", "/account/dashboard"]]) {
    await navigateByHeader(page, name);
    await expect(page).toHaveURL(new RegExp(`${path}$`));
    await expect(page.getByRole("heading", { level: 1 })).toBeVisible();
    await expectNoHorizontalScroll(page);
  }
});

test("the mobile menu announces its state and restores keyboard focus", async ({ page }) => {
  await page.setViewportSize({ width: 390, height: 844 });
  await page.goto("/");
  const menu = page.getByRole("button", { name: "Menu" });
  const navigation = page.getByRole("navigation", { name: "Primary navigation" });
  await expect(menu).toHaveAttribute("aria-expanded", "false");
  await expect(navigation).toBeHidden();
  await menu.click();
  await expect(menu).toHaveAttribute("aria-expanded", "true");
  await expect(navigation).toBeVisible();
  await page.keyboard.press("Escape");
  await expect(navigation).toBeHidden();
  await expect(menu).toBeFocused();
});

test("a cold payment-recovery visit offers sign-in without exposing account data", async ({ page }) => {
  await page.goto("/account/payment-method");
  const signIn = page.getByRole("link", { name: "Email me a sign-in link", exact: true });
  await expect(signIn).toBeVisible();
  await expect(signIn).toHaveAttribute("href", "/account/sign-in");
  await expect(page.getByRole("button", { name: "Continue to Stripe" })).toHaveCount(0);
  await expectNoHorizontalScroll(page);
});

test("removed legacy surfaces and proxy paths return not found", async ({ page, request }) => {
  for (const path of ["/impactgraph", "/operator", "/verifier", "/proof/old"]) {
    const response = await page.goto(path);
    expect(response?.status()).toBe(404);
    await expect(page.getByRole("heading", { name: "We couldn’t find this page" })).toBeVisible();
  }
  expect((await request.get("/api/claims")).status()).toBe(404);
});
