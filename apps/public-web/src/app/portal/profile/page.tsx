import { SiteFooter, SiteHeader } from "@/components/SiteChrome";
import { PortalProfileClient } from "@/components/portal/PortalProfileClient";
import { buildPropertyBrandCssVars } from "@/lib/branding";
import { loadPortalPropertyContext } from "@/lib/portal-server";

export const dynamic = "force-dynamic";

export default async function PortalProfilePage() {
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
        <PortalProfileClient />
      </main>
      <SiteFooter property={property} />
    </div>
  );
}
