import type { Metadata } from "next";
import { GiftFraming } from "@/components/donor/GiftFraming";
import { Page } from "@/components/donor/Page";
import { PasskeySignIn } from "@/components/donor/PasskeySignIn";
import { PasswordAccess } from "@/components/donor/PasswordAccess";
import { PhoneSignIn } from "@/components/donor/PhoneSignIn";
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
      <PhoneSignIn />
      <PasswordAccess />
      <p>
        <a href="/account/oauth/apple">Sign in with Apple</a> · <a href="/account/oauth/google">Sign in with Google</a>
      </p>
      <div className="callout">
        <h2>New to DAD?</h2>
        <p>
          Start with a gift of <GiftFraming minor={3000} />. We’ll email you a link to claim your account.
        </p>
        <a className="secondary" href="/donate">
          Give $1 a day
        </a>
        <p>
          <a className="quiet" href="/account/signup">
            Create an account without giving
          </a>
        </p>
      </div>
    </Page>
  );
}
