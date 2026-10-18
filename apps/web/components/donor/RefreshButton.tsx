"use client";
import { useRouter } from "next/navigation";
import { useTransition } from "react";

/** Re-renders the current server page, fetching its data again. */
export function RefreshButton({ children }: { children: React.ReactNode }) {
  const router = useRouter();
  const [pending, startTransition] = useTransition();
  return (
    <button
      type="button"
      className="secondary"
      disabled={pending}
      onClick={() => startTransition(() => router.refresh())}
    >
      {pending ? "Checking again…" : children}
    </button>
  );
}
