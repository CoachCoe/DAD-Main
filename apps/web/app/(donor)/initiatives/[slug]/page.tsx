import type { Metadata } from "next";
import Link from "next/link";
import { notFound } from "next/navigation";
import { Page } from "@/components/donor/Page";
import { ContentUnavailable, InitiativeFacts } from "@/components/donor/PublicContent";
import { loadInitiative } from "@/lib/publicContent";

async function initiative(slug: string) {
  const found = await loadInitiative(slug);
  return found === null ? null : { found };
}

export async function generateMetadata({ params }: PageProps<"/initiatives/[slug]">): Promise<Metadata> {
  const result = await initiative((await params).slug);
  return { title: result?.found?.title ?? "Planned initiative", description: result?.found?.summary };
}

export default async function InitiativePage({ params }: PageProps<"/initiatives/[slug]">) {
  const result = await initiative((await params).slug);
  if (result && !result.found) notFound();
  return (
    <Page
      eyebrow="Planned initiative"
      back={{ href: "/initiatives", label: "All initiatives" }}
      title={result?.found?.title ?? "Planned initiative"}
    >
      {result?.found ? (
        <>
          <InitiativeFacts initiative={result.found} />
          <Link className="button" href="/donate">
            Support DAD’s work
          </Link>
          <p className="note">
            Gifts support DAD as a whole and are not designated to this initiative. DAD launches in April 2027.
          </p>
        </>
      ) : (
        <ContentUnavailable />
      )}
    </Page>
  );
}
