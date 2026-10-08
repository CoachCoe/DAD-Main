import type { Metadata } from "next";
import Partners from "./Partners";

export const metadata: Metadata = {
  title: "Partner with DAD",
  description: "Register your organization’s interest in shaping DAD initiatives for the April 2027 launch.",
};

export default function Page() {
  return <Partners />;
}
