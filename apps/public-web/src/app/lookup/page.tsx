import { BookingLookupClient } from "@/components/BookingLookupClient";
import { SiteFooter, SiteHeader } from "@/components/SiteChrome";
import { buildPropertyBrandCssVars } from "@/lib/branding";
import { loadPortalPropertyContext } from "@/lib/portal-server";

export const dynamic = "force-dynamic";

export default async function BookingLookupPage() {
  const { property, propertySlug, portalEnabled } = await loadPortalPropertyContext();
  const brandVars = buildPropertyBrandCssVars(property?.branding);

  return (
    <div style={brandVars}>
      <SiteHeader
        propertyName={property?.name}
        region={property?.region}
        branding={property?.branding}
        propertySlug={propertySlug}
        portalEnabled={portalEnabled}
      />
      <main className="container section" style={{ maxWidth: "560px" }}>
        <BookingLookupClient portalEnabled={portalEnabled} />
      </main>
      <SiteFooter property={property} />
    </div>
  );
}
