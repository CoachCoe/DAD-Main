"use client";
import { useState } from "react";
import { api, failureMessage } from "@/lib/api";
import { Announcer } from "./Announcer";
export function PasswordAccess({ setup = false }: { setup?: boolean }) {
  const [busy, setBusy] = useState(false),
    [error, setError] = useState(""),
    [status, setStatus] = useState("");
  return (
    <details>
      <summary>{setup ? "Set or change your password" : "Sign in with email and password"}</summary>
      <form
        onSubmit={async (e) => {
          e.preventDefault();
          if (busy) return;
          const f = new FormData(e.currentTarget);
          setBusy(true);
          setError("");
          setStatus("");
          try {
            await api(setup ? "/donor/me/password" : "/donor/password/sign-in", {
              method: "POST",
              body: JSON.stringify(
                setup
                  ? { current_password: String(f.get("current") ?? ""), password: String(f.get("password")) }
                  : { email: String(f.get("email")), password: String(f.get("password")) },
              ),
            });
            if (setup) setStatus("Password saved.");
            else window.location.assign("/account/dashboard");
          } catch (c) {
            setError(failureMessage(c, "Account access could not be completed."));
          } finally {
            setBusy(false);
          }
        }}
      >
        {setup ? (
          <label>
            Current password (leave blank when setting your first password)
            <input name="current" type="password" autoComplete="current-password" />
          </label>
        ) : (
          <label>
            Email
            <input name="email" type="email" autoComplete="username" required />
          </label>
        )}
        <label>
          {setup ? "New password" : "Password"}
          <input
            name="password"
            type="password"
            autoComplete={setup ? "new-password" : "current-password"}
            minLength={setup ? 12 : undefined}
            maxLength={72}
            required
          />
        </label>
        <button type="submit" disabled={busy}>
          {setup ? "Save password" : "Sign in"}
        </button>
        <Announcer status={status} alert={error} />
      </form>
    </details>
  );
}
