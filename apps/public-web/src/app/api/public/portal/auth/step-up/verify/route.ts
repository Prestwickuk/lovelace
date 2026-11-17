import { NextRequest, NextResponse } from "next/server";
import { handlePortalBffError, PRIVATE_NO_STORE_HEADERS } from "@/lib/bff-response";
import { getPropertySlug } from "@/lib/lovelace-api";
import {
  assertNoQuerySecrets,
  assertSameOriginMutation,
  callLovelacePortal,
  enforcePortalBffRateLimit,
} from "@/lib/portal-bff";
import type { PortalSessionView } from "@/lib/types";

export async function POST(req: NextRequest) {
  const limited = enforcePortalBffRateLimit(req, "portal-step-up-verify", 15);
  if (limited) return limited;

  try {
    assertNoQuerySecrets(req);
    assertSameOriginMutation(req);
    const body = (await req.json()) as { code?: string };
    const res = await callLovelacePortal<{ session: PortalSessionView }>(
      req,
      "/auth/step-up/verify",
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
