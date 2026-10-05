import type { Metadata } from "next";
import SignIn from "./SignIn";

export const metadata: Metadata = { title: "Staff sign-in" };

export default function Page() {
  return <SignIn />;
}
