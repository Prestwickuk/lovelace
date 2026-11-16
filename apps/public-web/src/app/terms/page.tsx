import Link from "next/link";
import { SiteFooter, SiteHeader } from "@/components/SiteChrome";
import { fetchPublicCatalog } from "@/lib/lovelace-api";
import type { PublicPropertyView } from "@/lib/types";

export const dynamic = "force-dynamic";

export default async function TermsPage() {
  let prop: PublicPropertyView | undefined;
  try {
    prop = (await fetchPublicCatalog()).property;
  } catch {
    prop = undefined;
  }

  return (
    <>
      <SiteHeader propertyName={prop?.name} region={prop?.region} branding={prop?.branding} propertySlug={prop?.slug} />
      <main className="container section" style={{ maxWidth: "780px" }}>
        <div className="card-panel">
          <h1 className="section-title">Booking Terms &amp; Cancellation Policy</h1>
          <p style={{ color: "var(--text-muted)" }}>
            Policy version: <strong>{prop?.policy.booking_terms_version || "2026.1"}</strong>
          </p>
          <p>
            {prop?.policy.cancellation_policy ||
              "Cancellation terms are defined in the property booking policy."}
          </p>
          <h2 style={{ fontSize: "1.2rem", color: "var(--brand-forest)" }}>
            Arrivals, Quiet Hours &amp; Site Rules
          </h2>
          <ul>
            <li>Check-in is welcomed from {prop?.check_in_time || "15:00"}.</li>
            <li>Accommodation must be vacated by {prop?.check_out_time || "10:30"}.</li>
            <li>
              Quiet hours:{" "}
              {prop?.policy.quiet_hours || "Configured per property booking policy."}
            </li>
            <li>
              Fires &amp; BBQs:{" "}
              {prop?.policy.fire_policy || "Configured per property booking policy."}
            </li>
            {prop?.policy.pet_policy ? <li>Pets: {prop.policy.pet_policy}</li> : null}
          </ul>
          <Link href="/stays" className="btn btn-primary">
            Back to Availability
          </Link>
        </div>
      </main>
      <SiteFooter property={prop} />
    </>
  );
}
