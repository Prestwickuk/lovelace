import Link from "next/link";
import { notFound } from "next/navigation";
import { SiteFooter, SiteHeader } from "@/components/SiteChrome";
import { SearchBar } from "@/components/SearchBar";
import {
  defaultSearchDates,
  formatAmenity,
  formatCategoryLabel,
  formatMoney,
  formatPricingModel,
  getUnitTypeGallery,
} from "@/lib/format";
import { fetchAccommodationDetail, LovelaceApiError } from "@/lib/lovelace-api";

export const dynamic = "force-dynamic";

export default async function StayDetailPage({
  params,
  searchParams,
}: {
  params: Promise<{ code: string }>;
  searchParams: Promise<Record<string, string | string[] | undefined>>;
}) {
  const { code } = await params;
  const sp = await searchParams;
  const defaults = defaultSearchDates();

  let detail;
  try {
    detail = await fetchAccommodationDetail(code);
  } catch (err) {
    if (err instanceof LovelaceApiError && err.status === 404) {
      notFound();
    }
    throw err;
  }

  const { property: prop, unit_type: ut, extras } = detail;
  const gallery = getUnitTypeGallery(ut);
  const arrival =
    typeof sp.arrival === "string" && sp.arrival ? sp.arrival : defaults.arrival;
  const departure =
    typeof sp.departure === "string" && sp.departure
      ? sp.departure
      : defaults.departure;
  const adults = Math.max(1, Number(sp.adults || 2) || 2);
  const children = Math.max(0, Number(sp.children || 0) || 0);
  const infants = Math.max(0, Number(sp.infants || 0) || 0);

  const bookQuery = new URLSearchParams({
    unit_type: ut.code,
    arrival,
    departure,
    adults: String(adults),
    children: String(children),
    infants: String(infants),
  });

  return (
    <>
      <SiteHeader
        propertyName={prop.name}
        region={prop.region}
        branding={prop.branding}
        propertySlug={prop.slug}
        portalEnabled={Boolean(prop.policy?.guest_portal_enabled)}
      />

      <main className="container section">
        <div style={{ marginBottom: "1rem" }}>
          <Link href="/stays" style={{ color: "var(--brand-forest)", fontWeight: 600 }}>
            ← All Accommodation
          </Link>
        </div>

        <div className="two-col-booking">
          <div>
            <div className="stay-card" style={{ marginBottom: "1.5rem" }}>
              <div className="stay-card-img-wrap" style={{ aspectRatio: "16 / 9" }}>
                <img
                  src={gallery[0].url}
                  alt={gallery[0].alt}
                  className="stay-card-img"
                />
              </div>
            </div>

            <div className="card-panel" style={{ marginBottom: "1.5rem" }}>
              <span className="badge">{formatCategoryLabel(ut.category)}</span>
              <h1 className="section-title" style={{ marginTop: "0.55rem" }}>
                {ut.name}
              </h1>
              <p style={{ fontSize: "1.03rem", color: "var(--text-primary)" }}>
                {ut.description}
              </p>

              <h2 style={{ fontSize: "1.2rem", color: "var(--brand-forest)", marginTop: "1.5rem" }}>
                Capacity &amp; House Rules
              </h2>
              <ul style={{ color: "var(--text-muted)", paddingLeft: "1.2rem" }}>
                <li>
                  Up to <strong>{ut.max_guests} guests total</strong> ({ut.max_adults} adults max, {ut.max_children} children max, {ut.max_infants} infants max)
                </li>
                <li>
                  Included occupancy: up to {ut.base_capacity_adults} adults in base nightly rate
                </li>
                <li>
                  Minimum stay: {ut.min_nights} {ut.min_nights === 1 ? "night" : "nights"} (maximum {ut.max_nights} nights online)
                </li>
                <li>
                  Pets: {ut.pets_allowed ? `Welcomed (up to ${ut.max_pets} dog${ut.max_pets === 1 ? "" : "s"})` : "Not permitted in this accommodation"}
                </li>
                <li>
                  Check-in from <strong>{prop.check_in_time}</strong> · Check-out by{" "}
                  <strong>{prop.check_out_time}</strong>
                </li>
              </ul>

              <h2 style={{ fontSize: "1.2rem", color: "var(--brand-forest)", marginTop: "1.5rem" }}>
                Included Amenities
              </h2>
              <div className="chip-list">
                {ut.amenities.map((am) => (
                  <span key={am} className="chip">
                    {formatAmenity(am)}
                  </span>
                ))}
              </div>
            </div>

            {extras.length > 0 && (
              <div className="card-panel">
                <h2 style={{ fontSize: "1.2rem", color: "var(--brand-forest)" }}>
                  Optional Add-Ons Available at Checkout
                </h2>
                <div style={{ display: "grid", gap: "0.75rem" }}>
                  {extras.map((ex) => (
                    <div
                      key={ex.id}
                      style={{
                        display: "flex",
                        justifyContent: "space-between",
                        gap: "1rem",
                        borderBottom: "1px solid var(--border-subtle)",
                        paddingBottom: "0.65rem",
                      }}
                    >
                      <div>
                        <strong>{ex.name}</strong>
                        <div style={{ fontSize: "0.86rem", color: "var(--text-muted)" }}>
                          {ex.description}
                        </div>
                      </div>
                      <div style={{ textAlign: "right", whiteSpace: "nowrap" }}>
                        <strong>{formatMoney(ex.price_pence, prop.currency)}</strong>
                        <div style={{ fontSize: "0.8rem", color: "var(--text-muted)" }}>
                          {formatPricingModel(ex.pricing_model)}
                        </div>
                      </div>
                    </div>
                  ))}
                </div>
              </div>
            )}
          </div>

          <aside>
            <div className="card-panel" style={{ marginBottom: "1.25rem" }}>
              <div style={{ fontSize: "0.85rem", color: "var(--text-muted)" }}>
                Standard base nightly rate
              </div>
              <div
                style={{
                  fontSize: "1.85rem",
                  fontWeight: 700,
                  color: "var(--brand-forest)",
                  marginBottom: "0.75rem",
                }}
              >
                {formatMoney(ut.base_nightly_rate_pence, prop.currency)}{" "}
                <span style={{ fontSize: "0.9rem", fontWeight: 400 }}>/ night</span>
              </div>
              <p style={{ fontSize: "0.88rem", color: "var(--text-muted)", marginBottom: "1rem" }}>
                {prop.policy.cancellation_policy}
              </p>
              <Link
                href={`/book?${bookQuery.toString()}`}
                className="btn btn-accent"
                style={{ width: "100%" }}
              >
                Continue to Book ({arrival} → {departure})
              </Link>
            </div>

            <SearchBar
              initial={{
                arrival,
                departure,
                adults,
                children,
                infants,
                category: ut.category,
              }}
              targetPath="/stays"
              compact
            />
          </aside>
        </div>
      </main>

      <SiteFooter property={prop} />
    </>
  );
}
