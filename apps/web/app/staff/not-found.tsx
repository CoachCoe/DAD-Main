import type { Metadata } from "next";
import Link from "next/link";
import { Page } from "@/components/donor/Page";

export const metadata: Metadata = { title: "Page not found" };

export default function StaffNotFound() {
  return (
    <Page eyebrow="Not found" title="We couldn’t find this page">
      <p className="lead">This page isn’t part of the staff back office.</p>
      <div className="actions">
        <Link className="button" href="/staff">
          Go to staff home
        </Link>
      </div>
    </Page>
  );
}
