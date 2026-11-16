import Link from "next/link";
import { SiteFooter, SiteHeader } from "@/components/SiteChrome";
import { BookingWizard } from "@/components/BookingWizard";
import { defaultSearchDates } from "@/lib/format";
import {
  computePublicPricing,
  fetchAccommodationDetail,
  LovelaceApiError,
} from "@/lib/lovelace-api";

export const dynamic = "force-dynamic";

export default async function BookPage({
  searchParams,
}: {
  searchParams: Promise<Record<string, string | string[] | undefined>>;
}) {
  const sp = await searchParams;
  const defaults = defaultSearchDates();

  const unitTypeCode =
    typeof sp.unit_type === "string" && sp.unit_type ? sp.unit_type : "bell-tent";
  const arrival =
    typeof sp.arrival === "string" && sp.arrival ? sp.arrival : defaults.arrival;
  const departure =
    typeof sp.departure === "string" && sp.departure
      ? sp.departure
      : defaults.departure;
  const adults = Math.max(1, Number(sp.adults || 2) || 2);
  const childrenCount = Math.max(0, Number(sp.children || 0) || 0);
  const infants = Math.max(0, Number(sp.infants || 0) || 0);
  const cancelledReturn = sp.cancelled === "1";

  let detail;
  let quote;
  let loadError = "";

  try {
    detail = await fetchAccommodationDetail(unitTypeCode);
    const priced = await computePublicPricing({
      property_slug: detail.property.slug,
      unit_type_code: unitTypeCode,
      arrival_date: arrival,
      departure_date: departure,
      adults,
      children: childrenCount,
      infants,
      extras: [],
    });
    quote = priced.quote;
  } catch (err) {
    loadError =
      err instanceof LovelaceApiError
        ? err.message
        : "This stay cannot currently be quoted for those dates.";
  }

  return (
    <>
      <SiteHeader
        propertyName={detail?.property.name || "Tir Bach Campsite"}
        region={detail?.property.region || "Pembrokeshire, Wales"}
        branding={detail?.property.branding}
        propertySlug={detail?.property.slug}
      />

      <main className="container section">
        <div style={{ marginBottom: "1.25rem" }}>
          <Link href="/stays" style={{ color: "var(--brand-forest)", fontWeight: 600 }}>
            ← Change Dates or Accommodation
          </Link>
        </div>

        <h1 className="section-title">Complete Your Direct Booking</h1>

        {loadError || !detail || !quote ? (
          <div className="card-panel">
            <div className="alert alert-err" role="alert">
              {loadError || "Selected stay could not be loaded."}
            </div>
            <Link href="/stays" className="btn btn-primary">
              Search Available Dates
            </Link>
          </div>
        ) : (
          <BookingWizard
            property={detail.property}
            unitType={detail.unit_type}
            extras={detail.extras}
            initialQuote={quote}
            arrival={arrival}
            departure={departure}
            adults={adults}
            childrenCount={childrenCount}
            infants={infants}
            cancelledReturn={cancelledReturn}
          />
        )}
      </main>

      <SiteFooter property={detail?.property} />
    </>
  );
}
