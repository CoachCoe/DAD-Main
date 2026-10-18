import Link from "next/link";

/** What a signed-out visitor sees in place of private account data. */
export function SignInPrompt({ children }: { children: string }) {
  return (
    <div className="callout">
      <p>You’re signed out. We’ll email you a single-use link — no password needed.</p>
      <Link className="button" href="/account/sign-in">
        {children}
      </Link>
    </div>
  );
}
