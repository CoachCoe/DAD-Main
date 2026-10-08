import { execFile } from "node:child_process";
import { promisify } from "node:util";
import type { Page } from "@playwright/test";

/** The back office has its own host; browsers resolve every *.localhost name to loopback. */
export const staffOrigin = process.env.DONOR_STAFF_ORIGIN ?? "http://staff.localhost:3000";
export const staffURL = (path: string) => new URL(path, staffOrigin).href;

/** A built-in authenticator that verifies its user, like a phone or laptop with a screen lock. */
export async function addPasskeyDevice(page: Page) {
  const session = await page.context().newCDPSession(page);
  await session.send("WebAuthn.enable");
  await session.send("WebAuthn.addVirtualAuthenticator", { options: {
    protocol: "ctap2", transport: "internal", hasResidentKey: true, hasUserVerification: true, isUserVerified: true, automaticPresenceSimulation: true,
  } });
}

const staffCommand = (args: string[]) => promisify(execFile)("go", ["run", "./cmd/staff", ...args], {
  cwd: "../donor-api", env: { ...process.env, DONOR_STAFF_ORIGIN: staffOrigin },
});

/**
 * Issues an administrator invitation as an operator would: the first run provisions the
 * administrator, and later runs use break-glass recovery, which replaces earlier passkeys.
 */
export async function administratorInvitation(email: string): Promise<string> {
  const { stdout } = await staffCommand(["--email", email, "--name", "E2E Administrator"])
    .catch(() => staffCommand(["--recover", "--email", email]));
  const link = /https?:\/\/\S+\/staff\/activate#token=\S+/.exec(stdout)?.[0];
  if (!link) throw new Error("The staff command printed no invitation");
  return link;
}
