import type { Metadata } from "next";
import Link from "next/link";
import { notFound } from "next/navigation";
import { ContentUnavailable, InitiativeFacts } from "@/components/donor/PublicContent";
import { loadPublicContent } from "@/lib/publicContent";

async function initiative(slug: string) {
  const content = await loadPublicContent();
  return content && { found: content.initiatives.find((item) => item.slug === slug) };
}

export async function generateMetadata({ params }: PageProps<"/initiatives/[slug]">): Promise<Metadata> {
  const result = await initiative((await params).slug);
  return { title: result?.found?.title ?? "Planned initiative", description: result?.found?.summary };
}

export default async function InitiativePage({ params }: PageProps<"/initiatives/[slug]">) {
  const result = await initiative((await params).slug);
  if (result && !result.found) notFound();
  return <section className="panel">
    <span className="eyebrow">DAD · PLANNED INITIATIVE</span>
    <h1>{result?.found?.title ?? "Planned initiative"}</h1>
    <p><Link href="/initiatives">All initiatives</Link> · DAD launches in April 2027.</p>
    {result?.found ? <>
      <InitiativeFacts initiative={result.found} />
      <p><Link href="/donate">Support DAD’s work</Link> — gifts support DAD as a whole and are not designated to this initiative.</p>
    </> : <ContentUnavailable />}
  </section>;
}
