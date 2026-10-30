"use client";
import Link from "next/link";
import { useState } from "react";
import { Announcer } from "@/components/donor/Announcer";
import { Page } from "@/components/donor/Page";
import type { Schema } from "@/lib/api";
import { formatDateTime } from "@/lib/dates";
import { resourceMessages, useApiResource } from "@/lib/useApiResource";

export default function Deletions() {
  // Keyset paging: each entry is the cursor that opened a page, the first being none.
  const [cursors, setCursors] = useState<string[]>([]);
  const cursor = cursors.at(-1);
  const { resource, data, reload } = useApiResource<Schema<"AccountDeletionPage">>(
    `/donor/staff/deletions${cursor ? `?${new URLSearchParams({ cursor })}` : ""}`,
  );
  const messages = resourceMessages(resource, {
    loading: "Loading account deletions…",
    failure: "Account deletions could not be loaded right now. Please retry in a moment.",
    forbidden: "Account deletions require donors and money access.",
  });
  return (
    <Page wide eyebrow="Staff" back={{ href: "/staff", label: "Staff back office" }} title="Account deletions">
      <p>
        Donors delete their own accounts in the app, and the erasure happens at once: personal data is removed and gift
        records are kept for legal and tax purposes. There is no request to approve, so this is the record of what was
        done.
      </p>
      {resource.state === "signedOut" && (
        <p>
          <Link className="button" href="/staff/sign-in">
            Staff sign-in
          </Link>
        </p>
      )}
      {data &&
        (data.deletions.length ? (
          <>
            {/* Keyboard focus is required for horizontal scrolling in Safari. */}
            {/* biome-ignore lint/a11y/noNoninteractiveTabindex: this is an interactive scroll region. */}
            <section className="tableScroll" tabIndex={0} aria-label="Account deletions table">
              <table className="dataTable">
                <thead>
                  <tr>
                    <th scope="col">Account</th>
                    <th scope="col">Deleted</th>
                    <th scope="col" className="numeric">
                      Gift records kept
                    </th>
                  </tr>
                </thead>
                <tbody>
                  {data.deletions.map((deletion) => (
                    <tr key={deletion.donor_id}>
                      <th scope="row">
                        <Link href={`/staff/donors/${encodeURIComponent(deletion.donor_id)}`}>
                          <code>{deletion.donor_id}</code>
                        </Link>
                      </th>
                      <td>
                        <time dateTime={deletion.deleted_at}>{formatDateTime(deletion.deleted_at)}</time>
                      </td>
                      <td className="numeric">{deletion.retained_gifts.toLocaleString()}</td>
                    </tr>
                  ))}
                </tbody>
              </table>
            </section>
          </>
        ) : (
          <p>No accounts have been deleted.</p>
        ))}
      {data && (
        <p className="actions">
          {cursors.length > 0 && (
            <button type="button" className="secondary" onClick={() => setCursors(cursors.slice(0, -1))}>
              Newer deletions
            </button>
          )}
          {data.next_cursor !== null && (
            <button
              type="button"
              className="secondary"
              onClick={() => setCursors([...cursors, data.next_cursor ?? ""])}
            >
              Older deletions
            </button>
          )}
        </p>
      )}
      <Announcer status={messages.status} alert={messages.alert} />
      {resource.state !== "signedOut" && (
        <button type="button" className="secondary" onClick={reload}>
          Refresh account deletions
        </button>
      )}
    </Page>
  );
}
