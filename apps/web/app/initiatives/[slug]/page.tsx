import Link from "next/link";
import { PublicContent } from "@/components/donor/PublicContent";
export default async function InitiativePage({ params }: { params: Promise<{ slug: string }> }) {
  const { slug } = await params;
  return <><h1>Initiative plan</h1><p><Link href="/initiatives">All initiatives</Link> · DAD launches in April 2027.</p><PublicContent view="initiatives" slug={slug} /></>;
}
