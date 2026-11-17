import { SiteFooter, SiteHeader } from "@/components/SiteChrome";
import { PortalBookingDetailClient } from "@/components/portal/PortalBookingDetailClient";
import { buildPropertyBrandCssVars } from "@/lib/branding";
import { loadPortalPropertyContext } from "@/lib/portal-server";

export const dynamic = "force-dynamic";

export default async function PortalBookingDetailPage({
  params,
}: {
  params: Promise<{ ref: string }>;
}) {
  const { ref } = await params;
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
      <main className="container section">
        <PortalBookingDetailClient reference={ref} />
      </main>
      <SiteFooter property={property} />
    </div>
  );
}
