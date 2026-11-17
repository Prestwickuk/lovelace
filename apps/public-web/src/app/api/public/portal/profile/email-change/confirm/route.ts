import { NextRequest, NextResponse } from "next/server";
import { handlePortalBffError, PRIVATE_NO_STORE_HEADERS } from "@/lib/bff-response";
import { getPropertySlug } from "@/lib/lovelace-api";
import {
  assertNoQuerySecrets,
  assertSameOriginMutation,
  callLovelacePortal,
  enforcePortalBffRateLimit,
} from "@/lib/portal-bff";
import type { PortalProfileView } from "@/lib/types";

export async function POST(req: NextRequest) {
  const limited = enforcePortalBffRateLimit(req, "portal-email-change-confirm", 15);
  if (limited) return limited;

  try {
    assertNoQuerySecrets(req);
    assertSameOriginMutation(req);
    const body = (await req.json()) as { code?: string };
    const res = await callLovelacePortal<{ profile: PortalProfileView }>(
      req,
      "/profile/email-change/confirm",
      {
        method: "POST",
        body: JSON.stringify({
          property_slug: getPropertySlug(),
          code: body.code || "",
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
