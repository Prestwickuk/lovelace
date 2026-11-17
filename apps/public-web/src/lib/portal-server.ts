import "server-only";

import { fetchPublicCatalog, getPropertySlug } from "./lovelace-api";
import type { PublicPropertyView } from "./types";

export async function loadPortalPropertyContext(): Promise<{
  propertySlug: string;
  property?: PublicPropertyView;
  portalEnabled: boolean;
}> {
  const propertySlug = getPropertySlug();
  try {
    const catalog = await fetchPublicCatalog(propertySlug);
    return {
      propertySlug,
      property: catalog.property,
      portalEnabled: Boolean(catalog.property.policy?.guest_portal_enabled),
    };
  } catch {
    return {
      propertySlug,
      property: undefined,
      portalEnabled: false,
    };
  }
}
