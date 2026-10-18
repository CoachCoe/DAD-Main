"use client";

import Link from "next/link";
import type { Schema } from "@/lib/api";
import { useApiResource } from "@/lib/useApiResource";

/** Spares a signed-in donor from requesting a link they do not need. */
export function AlreadySignedIn() {
  const { data } = useApiResource<Schema<"Profile">>("/donor/me/profile");
  if (!data) return null;
  return (
    <div className="callout highlight">
      <p>
        You’re already signed in as <strong>{data.email}</strong>.
      </p>
      <Link className="button" href="/account/dashboard">
        Go to my dashboard
      </Link>
    </div>
  );
}
