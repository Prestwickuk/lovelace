import { EmbedBookingWidget } from "@/components/EmbedBookingWidget";
import { sanitizePropertySlug } from "@/lib/branding";
import { defaultSearchDates } from "@/lib/format";
import {
  fetchPublicCatalog,
  LovelaceApiError,
  searchPublicAvailability,
} from "@/lib/lovelace-api";
import type {
  PublicAvailabilityResponse,
  PublicCatalogResponse,
} from "@/lib/types";

export const dynamic = "force-dynamic";

export default async function EmbedPropertyBookingPage({
  params,
  searchParams,
}: {
  params: Promise<{ slug: string }>;
  searchParams: Promise<Record<string, string | string[] | undefined>>;
}) {
  const { slug: rawSlug } = await params;
  const sp = await searchParams;
  const cleanSlug = sanitizePropertySlug(rawSlug, "");

  if (!cleanSlug) {
    return (
      <main className="container section">
        <div className="card-panel" role="alert">
          <h1 style={{ fontSize: "1.25rem", marginBottom: "0.4rem" }}>
            Booking Widget Unavailable
          </h1>
          <p style={{ margin: 0, color: "var(--text-muted)" }}>
            Invalid property identifier in embed URL.
          </p>
        </div>
      </main>
    );
  }

  const defaults = defaultSearchDates();
  const arrival =
    typeof sp.arrival === "string" && sp.arrival ? sp.arrival : defaults.arrival;
  const departure =
    typeof sp.departure === "string" && sp.departure
      ? sp.departure
      : defaults.departure;
  const adults = Math.min(30, Math.max(1, Math.trunc(Number(sp.adults || 2) || 2)));
  const children = Math.min(
    30,
    Math.max(0, Math.trunc(Number(sp.children || 0) || 0)),
  );
  const infants = Math.min(
    30,
    Math.max(0, Math.trunc(Number(sp.infants || 0) || 0)),
  );

  let catalog: PublicCatalogResponse | null = null;
  let availability: PublicAvailabilityResponse | null = null;
  let initialError = "";

  try {
    catalog = await fetchPublicCatalog(cleanSlug);
  } catch (err) {
    initialError =
      err instanceof LovelaceApiError && err.status === 404
        ? "This property could not be found."
        : "Unable to connect to the booking service right now.";
  }

  if (!catalog) {
    return (
      <main className="container section">
        <div className="card-panel" role="alert">
          <h1 style={{ fontSize: "1.25rem", marginBottom: "0.4rem" }}>
            Booking Section Temporarily Unavailable
          </h1>
          <p style={{ margin: 0, color: "var(--text-muted)" }}>{initialError}</p>
        </div>
      </main>
    );
  }

  try {
    availability = await searchPublicAvailability({
      property_slug: cleanSlug,
      arrival_date: arrival,
      departure_date: departure,
      adults,
      children,
      infants,
    });
  } catch (err) {
    initialError =
      err instanceof LovelaceApiError
        ? err.message
        : "Could not check availability for those dates.";
  }

  return (
    <main aria-label={`${catalog.property.name} embedded booking section`}>
      <EmbedBookingWidget
        initialProperty={availability?.property || catalog.property}
        extras={catalog.extras || []}
        initialAvailability={availability}
        initialArrival={arrival}
        initialDeparture={departure}
        initialAdults={adults}
        initialChildren={children}
        initialInfants={infants}
        initialError={initialError}
      />
    </main>
  );
}
