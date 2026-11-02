"use client";

import { useEffect, useState } from "react";
import { foundingDaysLeft, foundingDeadlineLabel } from "@/lib/launch";

/**
 * Home is prerendered and its HTML is cached at the edge, so it can be served after the
 * deadline. The days-left count therefore only exists after hydration, and the markup
 * before that is an empty box of the line's height: nothing can be false on any date, and
 * the line fills in without moving the page. After the window closes the box stays empty
 * rather than collapsing, so a visitor never sees the layout jump.
 *
 * `now` is a prop only so tests can fix the date.
 */
export function FoundingLine({ now }: { now?: Date }) {
  const [daysLeft, setDaysLeft] = useState<number | null>(null);
  useEffect(() => {
    setDaysLeft(foundingDaysLeft(now ?? new Date()));
  }, [now]);

  return (
    <div className="foundingLine">
      {daysLeft !== null && (
        <>
          <svg aria-hidden="true" viewBox="0 0 24 24" width="16" height="16" fill="currentColor" focusable="false">
            <path d="M12 2.5l2.9 6.1 6.6.9-4.8 4.6 1.2 6.6L12 17.5l-5.9 3.2 1.2-6.6L2.5 9.5l6.6-.9z" />
          </svg>
          <span>
            Founding Donor window: {daysLeft === 1 ? "1 day" : `${daysLeft} days`} left · first gift by{" "}
            {foundingDeadlineLabel()}
          </span>
        </>
      )}
    </div>
  );
}
