import type { Metadata } from "next";
import Link from "next/link";
import { Page } from "@/components/donor/Page";
import SignIn from "./SignIn";

export const metadata: Metadata = { title: "Partner sign-in" };

export default function SignInPage() {
  return (
    <Page eyebrow="Partner portal" title="Sign in to your partner portal">
      <p className="lead">
        Enter your work email. We’ll email a single-use link that signs you in to your organization’s verified
        dashboard.
      </p>
      <SignIn />
      <p className="note">The link expires after 15 minutes. You don’t need a password.</p>
      <p>
        Not a partner yet? <Link href="/partner/apply">Apply to partner with DAD</Link>.
      </p>
    </Page>
  );
}
