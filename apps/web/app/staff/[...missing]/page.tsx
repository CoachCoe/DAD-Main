import { notFound } from "next/navigation";

// Unknown back-office paths, and every page proxy.ts does not serve on the staff host,
// answer not found inside the back office's frame.
export default function Missing() {
  notFound();
}
