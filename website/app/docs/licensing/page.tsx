import { redirect } from "next/navigation";

// The hand-written duplicate of docs/licensing.md lived here and had no drift
// gate — it had already diverged from the source in wording. The markdown is now
// the single source and is rendered at /docs/reference/licensing; this redirect
// keeps the old URL working rather than breaking a link that has been published.
export default function LicensingRedirect() {
  redirect("/docs/reference/licensing");
}
