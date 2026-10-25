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
      setError("This sign-in link is missing its token. Open the link from your email again.");
      return;
    }
    api("/donor/partner/accept-invite", { method: "POST", body: JSON.stringify({ invite: token }) })
      .then(() => window.location.assign("/partner"))
      .catch(() => setError("This sign-in link has already been used or has expired. Request a new one below."));
  }, [token]);

  return (
    <Page eyebrow="Partner portal" title={error ? "This link didn’t work" : "Signing you in…"}>
      {error ? (
        <p>
          <Link className="button" href="/partner/sign-in">
            Email me a new sign-in link
          </Link>
        </p>
      ) : (
        <p>Confirming your link and opening your partner dashboard.</p>
      )}
      <Announcer status={error ? "" : "Signing in…"} alert={error} />
    </Page>
  );
}
