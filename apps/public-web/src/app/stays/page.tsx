import Link from "next/link";
import { SiteFooter, SiteHeader } from "@/components/SiteChrome";
import { SearchBar } from "@/components/SearchBar";
import {
  defaultSearchDates,
  formatAmenity,
  formatCategoryLabel,
  formatMoney,
  formatStayDate,
  getUnitTypeGallery,
} from "@/lib/format";
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

export default async function StaysPage({
  searchParams,
}: {
  searchParams: Promise<Record<string, string | string[] | undefined>>;
}) {
  const sp = await searchParams;
  const defaults = defaultSearchDates();

  const arrival =
    typeof sp.arrival === "string" && sp.arrival ? sp.arrival : defaults.arrival;
  const departure =
    typeof sp.departure === "string" && sp.departure
      ? sp.departure
      : defaults.departure;
  const adults = Math.max(1, Number(sp.adults || 2) || 2);
  const children = Math.max(0, Number(sp.children || 0) || 0);
  const infants = Math.max(0, Number(sp.infants || 0) || 0);
  const category = typeof sp.category === "string" ? sp.category : "";

  const hasExplicitDates = Boolean(sp.arrival && sp.departure);

  let catalog: PublicCatalogResponse | null = null;
  let availability: PublicAvailabilityResponse | null = null;
  let searchError = "";

  try {
    catalog = await fetchPublicCatalog();
  } catch {
    searchError = "Unable to connect to the reservation system right now.";
  }

  if (catalog && arrival && departure) {
    try {
      availability = await searchPublicAvailability({
        arrival_date: arrival,
        departure_date: departure,
        adults,
        children,
        infants,
        category,
      });
    } catch (err) {
      if (err instanceof LovelaceApiError) {
        searchError = err.message;
      } else {
        searchError = "Could not check availability for those dates.";
      }
    }
  }

  const prop = availability?.property || catalog?.property;
  const results = availability?.items || [];

  const categories = [
    { code: "", label: "All Stays" },
    { code: "GLAMPING", label: "Bell Tents" },
    { code: "CABIN", label: "Shepherd's Hut" },
    { code: "TENT_PITCH", label: "Grass Pitches" },
    { code: "TOURING_PITCH", label: "Electric Hook-Up" },
  ];

  function buildFilterUrl(cat: string) {
    const q = new URLSearchParams({
      arrival,
      departure,
      adults: String(adults),
      children: String(children),
      infants: String(infants),
    });
    if (cat) q.set("category", cat);
    return `/stays?${q.toString()}`;
  }

  return (
    <>
      <SiteHeader
        propertyName={prop?.name || "Tir Bach Campsite"}
        region={prop?.region || "Pembrokeshire, Wales"}
        branding={prop?.branding}
        propertySlug={prop?.slug}
        portalEnabled={Boolean(prop?.policy?.guest_portal_enabled)}
      />

      <main className="container section">
        <div className="section-header" style={{ marginBottom: "1.25rem" }}>
          <div>
            <h1 className="section-title" style={{ marginBottom: "0.3rem" }}>
              Availability &amp; Accommodation
            </h1>
            <p className="section-sub">
              Showing real-time availability for {formatStayDate(arrival)} to{" "}
              {formatStayDate(departure)} · {adults}{" "}
              {adults === 1 ? "adult" : "adults"}
              {children > 0 ? `, ${children} children` : ""}
              {infants > 0 ? `, ${infants} infants` : ""}
            </p>
          </div>
        </div>

        <SearchBar
          initial={{ arrival, departure, adults, children, infants, category }}
        />

        <div
          className="chip-list"
          role="navigation"
          aria-label="Filter accommodation by type"
          style={{ marginBlock: "1.5rem" }}
        >
          {categories.map((c) => (
            <Link
              key={c.code || "all"}
              href={buildFilterUrl(c.code)}
              className="btn btn-secondary"
              style={{
                padding: "0.45rem 0.9rem",
                fontSize: "0.86rem",
                background:
                  category === c.code ? "var(--brand-forest)" : "#ffffff",
                color: category === c.code ? "#ffffff" : "var(--brand-forest)",
              }}
            >
              {c.label}
            </Link>
          ))}
        </div>

        {searchError && (
          <div className="alert alert-err" role="alert">
            {searchError}
          </div>
        )}

        {!searchError && availability && results.length === 0 && (
          <div className="card-panel" role="status">
            <h2 style={{ color: "var(--brand-forest)", marginBottom: "0.45rem" }}>
              No pitches or glamping units match those exact dates and party size
            </h2>
            <p style={{ color: "var(--text-muted)", marginBottom: "1rem" }}>
              Some accommodation requires a minimum 2-night or 3-night stay during
              peak weekends, or fits fewer guests per unit. Try adjusting your
              dates or clearing the category filter.
            </p>
            <div style={{ display: "flex", gap: "0.75rem", flexWrap: "wrap" }}>
              {category && (
                <Link href={buildFilterUrl("")} className="btn btn-secondary">
                  Show All Categories
                </Link>
              )}
              <Link href="/" className="btn btn-primary">
                Back to Campsite Home
              </Link>
            </div>
          </div>
        )}

        {results.length > 0 && (
          <div className="cards-grid-2">
            {results.map((item) => {
              const ut = item.unit_type;
              const gallery = getUnitTypeGallery(ut);
              const bookParams = new URLSearchParams({
                unit_type: ut.code,
                arrival: availability!.arrival_date,
                departure: availability!.departure_date,
                adults: String(adults),
                children: String(children),
                infants: String(infants),
              });
              return (
                <article key={ut.id} className="stay-card">
                  <div className="stay-card-img-wrap">
                    <img
                      src={gallery[0].url}
                      alt={gallery[0].alt}
                      className="stay-card-img"
                    />
                  </div>
                  <div className="stay-card-body">
                    <div
                      style={{
                        display: "flex",
                        justifyContent: "space-between",
                        alignItems: "center",
                        gap: "0.5rem",
                      }}
                    >
                      <span className="badge badge-ok">
                        {item.available_units}{" "}
                        {item.available_units === 1 ? "pitch/unit" : "pitches/units"}{" "}
                        available
                      </span>
                      <span className="badge">
                        {formatCategoryLabel(ut.category)}
                      </span>
                    </div>

                    <h2 style={{ margin: 0, fontSize: "1.4rem", color: "var(--brand-forest)" }}>
                      {ut.name}
                    </h2>

                    <p style={{ margin: 0, color: "var(--text-muted)", fontSize: "0.94rem" }}>
                      {ut.description}
                    </p>

                    <div className="chip-list">
                      <span className="chip">Max {ut.max_guests} guests</span>
                      <span className="chip">
                        {ut.pets_allowed ? "Dogs welcome" : "No pets"}
                      </span>
                      {ut.amenities.slice(0, 3).map((am) => (
                        <span key={am} className="chip">
                          {formatAmenity(am)}
                        </span>
                      ))}
                    </div>

                    <div
                      style={{
                        marginTop: "auto",
                        paddingTop: "0.85rem",
                        borderTop: "1px solid var(--border-subtle)",
                        display: "flex",
                        justifyContent: "space-between",
                        alignItems: "center",
                        gap: "1rem",
                        flexWrap: "wrap",
                      }}
                    >
                      <div>
                        <div
                          style={{
                            fontSize: "1.25rem",
                            fontWeight: 700,
                            color: "var(--brand-forest)",
                          }}
                        >
                          {formatMoney(item.total_pence, item.currency)}
                        </div>
                        <div style={{ fontSize: "0.82rem", color: "var(--text-muted)" }}>
                          {item.nights} {item.nights === 1 ? "night" : "nights"} ·{" "}
                          {item.balance_due_pence > 0
                            ? `${formatMoney(item.deposit_due_pence, item.currency)} deposit today`
                            : "Full payment at checkout"}
                        </div>
                      </div>

                      <div style={{ display: "flex", gap: "0.6rem" }}>
                        <Link
                          href={`/stays/${encodeURIComponent(ut.code)}?${bookParams.toString()}`}
                          className="btn btn-secondary"
                        >
                          Details
                        </Link>
                        <Link
                          href={`/book?${bookParams.toString()}`}
                          className="btn btn-accent"
                        >
                          Book Now
                        </Link>
                      </div>
                    </div>
                  </div>
                </article>
              );
            })}
          </div>
        )}

        {!hasExplicitDates && results.length > 0 && (
          <p style={{ color: "var(--text-muted)", fontSize: "0.86rem", marginTop: "1.25rem" }}>
            Showing sample dates 14 days from today. Adjust arrival and departure above for your exact trip.
          </p>
        )}
      </main>

      <SiteFooter property={prop} />
    </>
  );
}
