import type { Metadata } from "next";
import CompanyLanding from "./CompanyLanding";

export const metadata: Metadata = {
  title: "Give with your workplace",
  description: "Join your colleagues giving about a dollar a day with DAD.",
  robots: { index: false },
};

export default async function CompanyPage({ params }: PageProps<"/c/[code]">) {
  const { code } = await params;
  let decoded = code;
  try {
    decoded = decodeURIComponent(code);
  } catch {}
  return <CompanyLanding code={decoded} />;
}
