import { NextRequest, NextResponse } from "next/server";
import { handlePortalBffError, parseJsonObject, PRIVATE_NO_STORE_HEADERS } from "@/lib/bff-response";
import { getPropertySlug } from "@/lib/lovelace-api";
import {
  assertNoQuerySecrets,
  assertSameOriginMutation,
  callLovelacePortal,
  enforcePortalBffRateLimit,
} from "@/lib/portal-bff";
import type { PortalProfileView } from "@/lib/types";

export async function POST(req: NextRequest) {
  const limited = enforcePortalBffRateLimit(req, "portal-consent-update", 20);
  if (limited) return limited;

  try {
    assertNoQuerySecrets(req);
    assertSameOriginMutation(req);
    const body = await parseJsonObject<{ status?: string }>(req);
    const res = await callLovelacePortal<{ profile: PortalProfileView }>(
      req,
      "/consent",
      {
        method: "POST",
        body: JSON.stringify({
          property_slug: getPropertySlug(),
          status: body.status || "",
        }),
      },
      { requireSession: true, requireCsrf: true },
    );
    return NextResponse.json(res, {
      status: 200,
      headers: PRIVATE_NO_STORE_HEADERS,
    });
  } catch (err) {
    return handlePortalBffError(err);
  }
}
