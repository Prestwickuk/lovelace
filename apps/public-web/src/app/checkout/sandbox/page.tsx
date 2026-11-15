import { notFound } from "next/navigation";
import { SandboxCheckoutClient } from "@/components/SandboxCheckoutClient";

export const dynamic = "force-dynamic";

export default function SandboxCheckoutPage() {
  const flag = (process.env.LOVELACE_STRIPE_SANDBOX || "").trim().toLowerCase();
  if (process.env.NODE_ENV === "production" && flag !== "true") {
    notFound();
  }
  if (flag === "false") {
    notFound();
  }

  return <SandboxCheckoutClient />;
}
