import { SiteFrame } from "@/components/donor/SiteFrame";

export default function DonorLayout({ children }: { children: React.ReactNode }) {
  return <SiteFrame>{children}</SiteFrame>;
}
