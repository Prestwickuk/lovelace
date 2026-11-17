import { cookies } from "next/headers";
import { SiteFooter, SiteHeader } from "@/components/SiteChrome";
import { PortalLoginClient } from "@/components/portal/PortalLoginClient";
import { buildPropertyBrandCssVars } from "@/lib/branding";
import { GUEST_SESSION_COOKIE } from "@/lib/portal-bff";
import { loadPortalPropertyContext } from "@/lib/portal-server";

export const dynamic = "force-dynamic";

export default async function PortalPage() {
  const [{ property, propertySlug, portalEnabled }, cookieStore] = await Promise.all([
    loadPortalPropertyContext(),
    cookies(),
  ]);
  const hasSessionCookie = Boolean(cookieStore.get(GUEST_SESSION_COOKIE)?.value);
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
      <main className="container section" style={{ maxWidth: "680px" }}>
        <PortalLoginClient
          portalEnabled={portalEnabled}
          hasSessionCookie={hasSessionCookie}
        />
      </main>
      <SiteFooter property={property} />
    </div>
  );
}
