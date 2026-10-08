import type { Metadata } from "next";
import Profile from "./Profile";

export const metadata: Metadata = { title: "Profile and notifications" };

export default function Page() {
  return <Profile />;
}
