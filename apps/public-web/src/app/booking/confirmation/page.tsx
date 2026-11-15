import { SiteFooter, SiteHeader } from "@/components/SiteChrome";
import { ConfirmationViewClient } from "@/components/ConfirmationViewClient";
import { fetchPublicCatalog } from "@/lib/lovelace-api";
import type { PublicPropertyView } from "@/lib/types";

export const dynamic = "force-dynamic";

export default async function ConfirmationPage({
  searchParams,
}: {
  searchParams: Promise<Record<string, string | string[] | undefined>>;
}) {
  const sp = await searchParams;
  const ref = typeof sp.ref === "string" ? sp.ref : "";

  let prop: PublicPropertyView | undefined;
  try {
    const cat = await fetchPublicCatalog();
    prop = cat.property;
  } catch {
    prop = undefined;
  }

  return (
    <>
      <SiteHeader propertyName={prop?.name} region={prop?.region} />
      <main className="container section">
        <ConfirmationViewClient initialRef={ref} />
      </main>
      <SiteFooter property={prop} />
    </>
  );
}
