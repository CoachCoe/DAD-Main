"use client";
import { usePathname } from "next/navigation";
import { useEffect, useState } from "react";
import { analyticsAllowed, analyticsChoice, analyticsHasChoice, observe } from "@/lib/analytics";
export function AnalyticsChoice() {
  const path = usePathname(),
    [ready, setReady] = useState(false),
    [chosen, setChosen] = useState(false);
  useEffect(() => {
    setReady(true);
    setChosen(analyticsHasChoice());
  }, []);
  useEffect(() => {
    if (analyticsAllowed()) {
      observe("app_opened");
      if (path.startsWith("/r/"))
        observe("attribution_captured", {
          source: new URLSearchParams(location.search).get("via") === "irl" ? "IRL" : "link",
        });
      if (path.startsWith("/c/")) observe("attribution_captured", { source: "company" });
    }
  }, [path]);
  if (!ready) return null;
  return (
    <aside aria-label="Usage analytics" className="note">
      {!chosen ? (
        <>
          <p>
            Allow first-party usage analytics? DAD uses a temporary journey ID to measure signup, sharing and giving. No
            names, email addresses or payment details are sent. Observations are kept for 13 months. You can change this
            choice here.
          </p>
          <button
            type="button"
            onClick={() => {
              analyticsChoice(true);
              setChosen(true);
              observe("app_opened");
            }}
          >
            Allow usage analytics
          </button>{" "}
          <button
            type="button"
            onClick={() => {
              analyticsChoice(false);
              setChosen(true);
            }}
          >
            Decline usage analytics
          </button>
        </>
      ) : (
        <button
          type="button"
          onClick={() => {
            analyticsChoice(false);
            setChosen(false);
          }}
        >
          Change analytics preference
        </button>
      )}
    </aside>
  );
}
