"use client";

import Link from "next/link";
import { useEffect, useState } from "react";
import { Announcer } from "@/components/donor/Announcer";
import { Page } from "@/components/donor/Page";
import { api, failureMessage, type ResponseBody } from "@/lib/api";

type Capture = ResponseBody<"/donor/company/{code}", "post">;

/**
 * The landing for an employer's company code (D-3). Saving it from the visitor's browser sets
 * the attribution cookie; account proof is required before linking the employer (EM-2).
 */
export default function CompanyLanding({ code }: { code: string }) {
  const [captured, setCaptured] = useState<boolean | null>(null);
  const [error, setError] = useState("");
  useEffect(() => {
    const controller = new AbortController();
    api<Capture>(`/donor/company/${encodeURIComponent(code)}`, {
      method: "POST",
      body: "{}",
      signal: controller.signal,
    })
      .then((result) => setCaptured(result.captured))
      .catch((cause) => {
        if (!controller.signal.aborted) setError(failureMessage(cause, "We couldn’t save your company code."));
      });
    return () => controller.abort();
  }, [code]);

  if (captured === false)
    return (
      <Page eyebrow="Workplace giving" title="This company code isn’t valid">
        <p className="lead">Check the code with your employer. You can still give, and add the code later.</p>
        <Link className="button full" href="/donate">
          Give without a company code
        </Link>
      </Page>
    );
  return (
    <Page eyebrow="Workplace giving" title="Give with your workplace">
      <p className="lead">
        Your colleagues give about a dollar a day with DAD, and your employer sees the combined impact. Your own gifts,
        streak and badges stay yours: your employer only ever sees totals for groups of five or more.
      </p>
      <Link className="button full" href="/donate">
        Start giving $1 a day
      </Link>
      <p className="note">Sign in or claim your account to link your giving to this workplace.</p>
      <p className="note">
        Company code <span className="hash">{code}</span>
        {captured ? " is saved for this browser." : "."} Already giving? Add it under Employer and team in your account.
      </p>
      <Announcer status={captured ? "Company code saved." : ""} alert={error} />
    </Page>
  );
}
