"use client";

import { useEffect, useRef, useState } from "react";
import { Announcer } from "@/components/donor/Announcer";
import { ApiError, api, failureMessage, type Schema } from "@/lib/api";
import { formatDate, formatDateTime } from "@/lib/dates";
import { resourceMessages, useApiResource } from "@/lib/useApiResource";

type DeviceSession = Schema<"DeviceSession">;
type SessionList = Schema<"SessionList">;

const apps = { IOS: "iOS app", ANDROID: "Android app" } as const;

/** Where the donor is signed in, with a way to sign out of every other place. */
export function Sessions({ onSignedOut }: { onSignedOut: () => void }) {
  const { resource, data, failed, reload, setData } = useApiResource<SessionList>("/donor/me/sessions");
  const [busy, setBusy] = useState(false);
  const [message, setMessage] = useState("");
  const [error, setError] = useState("");
  const heading = useRef<HTMLHeadingElement>(null);
  const buttons = useRef(new Map<string, HTMLButtonElement>());
  const focusAfterUpdate = useRef<string | null>(null);
  useEffect(() => {
    if (resource.state === "signedOut") onSignedOut();
  }, [resource.state, onSignedOut]);
  // A signed-out row takes its focused button with it: land on the row that took its place, or the heading.
  useEffect(() => {
    if (!data || focusAfterUpdate.current === null) return;
    (buttons.current.get(focusAfterUpdate.current) ?? heading.current)?.focus();
    focusAfterUpdate.current = null;
  }, [data]);
  const messages = resourceMessages(resource, {
    loading: "Loading where you’re signed in…",
    failure: "Your signed-in devices could not be loaded right now. Please retry in a moment.",
  });
  const others = data?.sessions.filter((session) => !session.current) ?? [];

  async function signOut(path: string, done: string, focus: (remaining: DeviceSession[]) => string) {
    if (busy) return;
    setBusy(true);
    setError("");
    setMessage("");
    try {
      const updated = await api<SessionList>(path, { method: "POST" });
      focusAfterUpdate.current = focus(updated.sessions.filter((session) => !session.current));
      setData(updated);
      setMessage(done);
    } catch (cause) {
      if (cause instanceof ApiError && cause.status === 401) onSignedOut();
      else setError(failureMessage(cause, "Sign-out was not confirmed. Refresh the list to check before retrying."));
    } finally {
      setBusy(false);
    }
  }
  const signOutOne = (session: DeviceSession) => {
    const position = others.indexOf(session);
    signOut(
      `/donor/me/sessions/${session.id}/revoke`,
      `Signed out of ${session.label}.`,
      (remaining) => remaining[Math.min(position, remaining.length - 1)]?.id ?? "",
    );
  };
  const signOutOthers = () =>
    signOut("/donor/me/sessions/revoke-others", "Signed out of every other device.", () => "");
  const retry = () => {
    setError("");
    setMessage("");
    reload();
  };

  return (
    <section aria-labelledby="sessions-heading">
      <h2 id="sessions-heading" ref={heading} tabIndex={-1}>
        Where you’re signed in
      </h2>
      {data && (
        <>
          <ul className="rows">
            {data.sessions.map((session) => (
              <li key={session.id}>
                <div className="session">
                  <p>
                    <strong>{session.label}</strong>
                    {session.current && <span className="badge current">This device</span>}
                  </p>
                  <p className="note">
                    {session.client === "APP" && <>{session.platform ? apps[session.platform] : "DAD app"} · </>}
                    Signed in <time dateTime={session.created_at}>{formatDate(session.created_at)}</time> ·{" "}
                    {session.current ? (
                      "Active now"
                    ) : (
                      <>
                        Last active <time dateTime={session.last_used_at}>{formatDateTime(session.last_used_at)}</time>
                      </>
                    )}
                  </p>
                </div>
                {!session.current && (
                  <button
                    type="button"
                    className="quiet"
                    disabled={busy}
                    onClick={() => signOutOne(session)}
                    ref={(button) => {
                      if (button) buttons.current.set(session.id, button);
                      return () => {
                        buttons.current.delete(session.id);
                      };
                    }}
                  >
                    Sign out{" "}
                    <span className="visuallyHidden">
                      {session.label}, last active {formatDateTime(session.last_used_at)}
                    </span>
                  </button>
                )}
              </li>
            ))}
          </ul>
          {others.length > 0 ? (
            <button type="button" className="secondary" disabled={busy} onClick={signOutOthers}>
              Sign out of all other devices
            </button>
          ) : (
            <p className="note">You’re not signed in anywhere else.</p>
          )}
        </>
      )}
      <Announcer status={messages.status || message} alert={error || messages.alert} />
      {(failed || error) && (
        <button type="button" className="secondary" disabled={busy} onClick={retry}>
          Refresh signed-in devices
        </button>
      )}
    </section>
  );
}
