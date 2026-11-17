import Link from "next/link";
import { SiteFooter, SiteHeader } from "@/components/SiteChrome";
import { fetchPublicCatalog } from "@/lib/lovelace-api";
import type { PublicPropertyView } from "@/lib/types";

export const dynamic = "force-dynamic";

export default async function PrivacyPage() {
  let prop: PublicPropertyView | undefined;
  try {
    prop = (await fetchPublicCatalog()).property;
  } catch {
    prop = undefined;
  }

  return (
    <>
      <SiteHeader
        propertyName={prop?.name}
        region={prop?.region}
        branding={prop?.branding}
        propertySlug={prop?.slug}
        portalEnabled={Boolean(prop?.policy?.guest_portal_enabled)}
      />
      <main className="container section" style={{ maxWidth: "780px" }}>
        <div className="card-panel">
          <h1 className="section-title">Guest Privacy Notice</h1>
          {prop?.policy.privacy_notice ? (
            <p>{prop.policy.privacy_notice}</p>
          ) : (
            <p>
              {prop?.name || "This accommodation property"} collects guest contact details
              solely to confirm your reservation, coordinate check-in instructions, and
              manage any cancellation or stay requests you initiate.
            </p>
          )}
          <p>
            Online card details are processed directly by Stripe via hosted Checkout
            Sessions. {prop?.name || "The property"} and LOVELACE never store raw payment
            card numbers.
          </p>
          {prop?.contact_email ? (
            <p style={{ color: "var(--text-muted)" }}>
              For data access or deletion requests, contact {prop.contact_email}.
            </p>
          ) : null}
          <Link href="/" className="btn btn-primary">
            Return to Home
          </Link>
        </div>
      </main>
      <SiteFooter property={prop} />
    </>
  );
}
