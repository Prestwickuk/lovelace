import { notFound } from "next/navigation";
import { SandboxCheckoutClient } from "@/components/SandboxCheckoutClient";
import { isStripeSandboxAllowed } from "@/lib/lovelace-api";

export const dynamic = "force-dynamic";

export default function SandboxCheckoutPage() {
  if (!isStripeSandboxAllowed()) {
    notFound();
  }

  return <SandboxCheckoutClient />;
}
