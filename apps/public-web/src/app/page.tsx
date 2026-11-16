import Link from "next/link";
import { SiteFooter, SiteHeader } from "@/components/SiteChrome";
import { SearchBar } from "@/components/SearchBar";
import {
  defaultSearchDates,
  formatAmenity,
  formatCategoryLabel,
  formatMoney,
  getUnitTypeGallery,
} from "@/lib/format";
import { fetchPublicCatalog } from "@/lib/lovelace-api";
import type { PublicCatalogResponse } from "@/lib/types";

export const dynamic = "force-dynamic";

export default async function HomePage() {
  let catalog: PublicCatalogResponse | null = null;
  let loadError = "";

  try {
    catalog = await fetchPublicCatalog();
  } catch {
    loadError =
      "We are updating live campsite availability right now. Please try searching again in a moment.";
  }

  const prop = catalog?.property;
  const unitTypes = catalog?.unit_types || [];
  const extras = catalog?.extras || [];
  const defaults = defaultSearchDates();

  return (
    <>
      <SiteHeader
        propertyName={prop?.name || "Tir Bach Campsite"}
        region={prop?.region || "Pembrokeshire, Wales"}
        branding={prop?.branding}
        propertySlug={prop?.slug}
      />

      <main>
        <section className="hero-section">
          <img
            src="/images/hero-pembrokeshire.jpg"
            alt={`${prop?.name || "Tir Bach Campsite"} overlooking ${prop?.region || "Pembrokeshire, Wales"}`}
            className="hero-bg-img"
          />
          <div className="hero-scrim" aria-hidden="true" />
          <div className="container hero-content">
            <span className="hero-eyebrow">
              {prop?.region || "Pembrokeshire, Wales"} · Direct Bookings
            </span>
            <h1 className="hero-title">
              {prop?.name || "Tir Bach Campsite"} — Coastal Stays &amp; Meadow Pitches
            </h1>
            <p className="hero-lead">
              {prop?.public_description ||
                prop?.tagline ||
                "Search live availability, view transparent rates, and reserve pitches or glamping stays directly with the property."}
            </p>
          </div>
        </section>

        <div className="container search-bar-shell">
          <SearchBar
            initial={{
              arrival: defaults.arrival,
              departure: defaults.departure,
              adults: 2,
              children: 0,
              infants: 0,
            }}
          />
        </div>

        <section className="section container" aria-labelledby="stays-heading">
          <div className="section-header">
            <div>
              <h2 id="stays-heading" className="section-title">
                Pitches &amp; Glamping Accommodation
              </h2>
              <p className="section-sub">
                Live inventory and pricing loaded directly from property availability settings.
              </p>
            </div>
            <Link href="/stays" className="btn btn-secondary">
              Search Live Availability
            </Link>
          </div>

          {loadError && (
            <div className="alert alert-warn" role="status">
              {loadError}
            </div>
          )}

          <div className="cards-grid-2">
            {unitTypes.map((ut) => {
              const gallery = getUnitTypeGallery(ut);
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
                        gap: "0.75rem",
                      }}
                    >
                      <span className="badge">
                        {formatCategoryLabel(ut.category)}
                      </span>
                      <span
                        style={{
                          fontWeight: 700,
                          color: "var(--brand-forest)",
                        }}
                      >
                        From {formatMoney(ut.base_nightly_rate_pence, prop?.currency)}{" "}
                        <small style={{ fontWeight: 400, color: "var(--text-muted)" }}>
                          / night
                        </small>
                      </span>
                    </div>

                    <h3 style={{ margin: 0, fontSize: "1.4rem", color: "var(--brand-forest)" }}>
                      {ut.name}
                    </h3>

                    <p style={{ margin: 0, color: "var(--text-muted)", fontSize: "0.95rem" }}>
                      {ut.description}
                    </p>

                    <div className="chip-list">
                      <span className="chip">Up to {ut.max_guests} guests</span>
                      <span className="chip">
                        Min {ut.min_nights} {ut.min_nights === 1 ? "night" : "nights"}
                      </span>
                      <span className="chip">
                        {ut.pets_allowed ? "Dogs welcome" : "Pet-free"}
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
                        paddingTop: "0.65rem",
                        display: "flex",
                        gap: "0.65rem",
                        flexWrap: "wrap",
                      }}
                    >
                      <Link
                        href={`/stays/${encodeURIComponent(ut.code)}`}
                        className="btn btn-secondary"
                      >
                        Explore Details
                      </Link>
                      <Link
                        href={`/stays?arrival=${defaults.arrival}&departure=${defaults.departure}&adults=2&category=${encodeURIComponent(ut.category)}`}
                        className="btn btn-primary"
                      >
                        Check Dates
                      </Link>
                    </div>
                  </div>
                </article>
              );
            })}
          </div>
        </section>

        <section
          id="facilities"
          className="section"
          style={{ background: "var(--bg-tint)", borderBlock: "1px solid var(--border-subtle)" }}
          aria-labelledby="facilities-heading"
        >
          <div className="container">
            <div className="section-header">
              <div>
                <h2 id="facilities-heading" className="section-title">
                  Site Policies &amp; Stay Information
                </h2>
                <p className="section-sub">
                  Verified property rules, check-in schedules, and bookable add-ons configured by the host.
                </p>
              </div>
            </div>

            <div className="cards-grid-3">
              <div className="card-panel">
                <h3 style={{ color: "var(--brand-forest)" }}>Check-In &amp; Quiet Hours</h3>
                <p style={{ margin: 0, color: "var(--text-muted)", fontSize: "0.95rem" }}>
                  Check-in from <strong>{prop?.check_in_time || "15:00"}</strong> and check-out by{" "}
                  <strong>{prop?.check_out_time || "10:30"}</strong>.{" "}
                  {prop?.policy.quiet_hours
                    ? prop.policy.quiet_hours
                    : "Specific quiet-hour guidance is confirmed in your booking policy by the property owner."}
                </p>
              </div>
              <div className="card-panel">
                <h3 style={{ color: "var(--brand-forest)" }}>Fires, Pets &amp; Site Rules</h3>
                <p style={{ margin: 0, color: "var(--text-muted)", fontSize: "0.95rem" }}>
                  {prop?.policy.fire_policy || "Campfire and BBQ rules are set in property policy before launch."}{" "}
                  {prop?.policy.pet_policy || "Pet eligibility varies by unit type and is shown per accommodation option."}
                </p>
              </div>
              <div className="card-panel">
                <h3 style={{ color: "var(--brand-forest)" }}>Optional Bookable Extras</h3>
                <p style={{ margin: 0, color: "var(--text-muted)", fontSize: "0.95rem" }}>
                  {extras.length > 0
                    ? `Available at booking: ${extras.map((ex) => ex.name).join(", ")}.`
                    : "Optional extras appear during booking when enabled by the property."}
                </p>
              </div>
            </div>
          </div>
        </section>

        <section id="location" className="section container" aria-labelledby="location-heading">
          <div className="cards-grid-2" style={{ alignItems: "center" }}>
            <div>
              <h2 id="location-heading" className="section-title">
                Finding Us &amp; Check-In Hours
              </h2>
              <p style={{ color: "var(--text-muted)" }}>
                <strong>Address:</strong>{" "}
                {[prop?.address_line1, prop?.town, prop?.postcode, prop?.region]
                  .filter(Boolean)
                  .join(", ") || "Pembrokeshire, Wales"}
                <br />
                <strong>Check-in:</strong> From {prop?.check_in_time || "15:00"} ·{" "}
                <strong>Departure:</strong> By {prop?.check_out_time || "10:30"}
              </p>
              <p style={{ color: "var(--text-muted)", marginBottom: "1.25rem" }}>
                {prop?.contact_email || prop?.contact_phone ? (
                  <>
                    Questions before travelling? Reach the campsite hosts directly
                    {prop?.contact_email ? ` on ${prop.contact_email}` : ""}
                    {prop?.contact_email && prop?.contact_phone ? " or " : ""}
                    {prop?.contact_phone ? prop.contact_phone : ""}.
                  </>
                ) : (
                  "Property host contact details are confirmed in your booking confirmation."
                )}
              </p>
              <Link href="/lookup" className="btn btn-secondary">
                Retrieve Existing Booking
              </Link>
            </div>
            <div className="card-panel">
              <h3 style={{ color: "var(--brand-forest)" }}>Booking &amp; Deposit Policy</h3>
              <p style={{ color: "var(--text-muted)", fontSize: "0.94rem", marginBottom: "0.75rem" }}>
                {prop?.policy.cancellation_policy ||
                  "Cancellation terms are configured per property policy and shown before checkout."}
              </p>
              <Link href="/terms" style={{ fontWeight: 600, color: "var(--brand-forest)" }}>
                Read full booking terms →
              </Link>
            </div>
          </div>
        </section>
      </main>

      <SiteFooter property={prop} />
    </>
  );
}
