import type { Metadata } from "next";
import { Page } from "@/components/donor/Page";
import { PasskeySignIn } from "@/components/donor/PasskeySignIn";
import { SignInForm } from "@/components/donor/SignInForm";
import { AlreadySignedIn } from "./AlreadySignedIn";

export const metadata: Metadata = { title: "Claim your account or sign in" };

export default function SignInPage() {
  return (
    <Page eyebrow="My account" title="Your donor account">
      <AlreadySignedIn />
      <p className="lead">
        Use the email you entered when giving. We’ll email a single-use link to claim your account or sign in.
      </p>
      <SignInForm />
      <p className="note">The link expires after 15 minutes. You don’t need a password.</p>
      <PasskeySignIn />
    </Page>
  );
}
