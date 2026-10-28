"use client";
import { useState } from "react";
import type { Schema } from "@/lib/api";
import { formatMoney } from "@/lib/money";

type Team = Schema<"TeamLeaderboardRow">;

/** Minutes as hours to one decimal place, e.g. 90 → "1.5 hours". */
export function hours(minutes: number): string {
  const value = Math.round(minutes / 6) / 10;
  return `${value.toLocaleString("en-US")} ${value === 1 ? "hour" : "hours"}`;
}

const teamViews = {
  giving: { label: "Combined active giving", sort: (t: Team) => t.annualized_minor },
  streak: { label: "Average streak", sort: (t: Team) => t.avg_streak_days },
  volunteer: { label: "Volunteer hours", sort: (t: Team) => t.volunteer_minutes },
} as const;

/** The company team leaderboard; the partner sees it in Our People and employees see it in their account. */
export function TeamLeaderboard({ teams }: { teams: Team[] }) {
  const [view, setView] = useState<keyof typeof teamViews>("giving");
  const ranked = [...teams].sort((a, b) => teamViews[view].sort(b) - teamViews[view].sort(a));
  return (
    <>
      <label>
        Rank teams by
        <select value={view} onChange={(event) => setView(event.target.value as keyof typeof teamViews)}>
          {Object.entries(teamViews).map(([key, v]) => (
            <option key={key} value={key}>
              {v.label}
            </option>
          ))}
        </select>
      </label>
      {/* Keyboard focus is required for horizontal scrolling in Safari. */}
      {/* biome-ignore lint/a11y/noNoninteractiveTabindex: this is an interactive scroll region. */}
      <section className="tableScroll" tabIndex={0} aria-label="Team leaderboard table">
        <table className="dataTable">
          <caption className="visuallyHidden">Team leaderboard ranked by {teamViews[view].label.toLowerCase()}</caption>
          <thead>
            <tr>
              <th scope="col">Rank</th>
              <th scope="col">Team</th>
              <th scope="col" className="numeric">
                Giving a year
              </th>
              <th scope="col" className="numeric">
                Average streak
              </th>
              <th scope="col" className="numeric">
                Volunteer hours
              </th>
            </tr>
          </thead>
          <tbody>
            {ranked.map((t, index) => (
              <tr key={t.team_id}>
                <td>{index + 1}</td>
                <td>{t.name}</td>
                <td className="numeric">{formatMoney(t.annualized_minor)}</td>
                <td className="numeric">{t.avg_streak_days} days</td>
                <td className="numeric">{hours(t.volunteer_minutes)}</td>
              </tr>
            ))}
          </tbody>
        </table>
      </section>
      <p className="note">Teams with fewer than 5 members are not shown.</p>
    </>
  );
}
