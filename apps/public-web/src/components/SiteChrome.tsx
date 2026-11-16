import Link from "next/link";
import { buildPropertyBrandCssVars, getPublicLogoSrc } from "@/lib/branding";
import type { PublicPropertyBranding, PublicPropertyView } from "@/lib/types";

export function SiteHeader({
  propertyName = "Tir Bach Campsite",
  region = "Pembrokeshire, Wales",
  branding,
  propertySlug = "tir-bach",
}: {
  propertyName?: string;
  region?: string;
  branding?: PublicPropertyBranding;
  propertySlug?: string;
}) {
  const title = branding?.display_name || propertyName;
  const sub = branding?.region_label || region;
  const logoSrc = getPublicLogoSrc(branding, propertySlug);
  const cssVars = buildPropertyBrandCssVars(branding);

  return (
    <header className="site-header" style={cssVars}>
      <div className="container site-header-inner">
        <Link href="/" className="brand-mark">
          {logoSrc ? (
            <img
              src={logoSrc}
              alt={branding?.logo_alt || `${title} logo`}
              className="brand-mark-logo"
            />
          ) : null}
          <span className="brand-mark-text">
            <span className="brand-mark-title">{title}</span>
            <span className="brand-mark-sub">{sub}</span>
          </span>
        </Link>

        <nav className="site-nav" aria-label="Main navigation">
          <Link href="/stays">Accommodation &amp; Availability</Link>
          <Link href="/#facilities">Policies &amp; Stay Info</Link>
          <Link href="/#location">Location</Link>
          <Link href="/lookup">Manage Booking</Link>
          <Link href="/stays" className="btn btn-primary" style={{ padding: "0.52rem 1rem" }}>
            Book Direct
          </Link>
        </nav>
      </div>
    </header>
  );
}

export function SiteFooter({ property }: { property?: PublicPropertyView }) {
  const name = property?.name || "Tir Bach Campsite";
  const email = property?.contact_email || "";
  const phone = property?.contact_phone || "";
  const addr = [property?.address_line1, property?.town, property?.postcode]
    .filter(Boolean)
    .join(", ");

  return (
    <footer className="site-footer">
      <div className="container">
        <div className="footer-grid">
          <div>
            <h3 style={{ color: "#ffffff", marginBottom: "0.5rem" }}>{name}</h3>
            <p style={{ margin: 0, color: "#c9d4ca", maxWidth: "36ch", fontSize: "0.94rem" }}>
              {property?.tagline || "Direct online accommodation booking."}
            </p>
          </div>
          <div>
            <h4 style={{ color: "#ffffff", marginBottom: "0.5rem" }}>Contact &amp; Arrivals</h4>
            <p style={{ margin: 0, fontSize: "0.92rem", color: "#c9d4ca" }}>
              {addr || property?.region || "Location confirmed after booking"}
              {email ? (
                <>
                  <br />
                  Email: <a href={`mailto:${email}`}>{email}</a>
                </>
              ) : null}
              {phone ? (
                <>
                  <br />
                  Telephone: {phone}
                </>
              ) : null}
            </p>
          </div>
          <div>
            <h4 style={{ color: "#ffffff", marginBottom: "0.5rem" }}>Guest Information</h4>
            <ul style={{ listStyle: "none", padding: 0, margin: 0, fontSize: "0.92rem", lineHeight: 1.9 }}>
              <li>
                <Link href="/stays">Search Live Availability</Link>
              </li>
              <li>
                <Link href="/lookup">Find My Booking</Link>
              </li>
              <li>
                <Link href="/terms">Booking Terms &amp; Cancellations</Link>
              </li>
              <li>
                <Link href="/privacy">Privacy Notice</Link>
              </li>
            </ul>
          </div>
        </div>

        <div className="footer-bottom">
          <span>© {new Date().getFullYear()} {name}. Direct booking site.</span>
          <span>Booking system powered by LOVELACE</span>
        </div>
      </div>
    </footer>
  );
}
