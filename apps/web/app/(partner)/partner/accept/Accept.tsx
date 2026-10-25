"use client";
import Link from "next/link";
import { useEffect, useRef, useState } from "react";
import { Announcer } from "@/components/donor/Announcer";
import { Page } from "@/components/donor/Page";
import { api } from "@/lib/api";
import { useHashToken } from "@/lib/useHashToken";

export default function Accept() {
  const { token } = useHashToken();
  const [error, setError] = useState("");
  const started = useRef(false);

  useEffect(() => {
    if (token === undefined || started.current) return;
    started.current = true;
    if (!token) {
      setError("This sign-in link is missing its token. Open the link from your invitation email again.");
      return;
    }
    api("/donor/partner/accept-invite", { method: "POST", body: JSON.stringify({ invite: token }) })
      .then(() => window.location.assign("/partner"))
      .catch(() => setError("This invitation link is invalid or has expired. Ask your administrator to resend it."));
  }, [token]);

  return (
    <Page eyebrow="Partner portal" title="Signing you in…">
      <p>Confirming your invitation and opening your partner dashboard.</p>
      {error && (
        <p>
          <Link href="/partner/apply">Apply to partner with DAD</Link>
        </p>
      )}
      <Announcer status={error ? "" : "Signing in…"} alert={error} />
    </Page>
  );
}
