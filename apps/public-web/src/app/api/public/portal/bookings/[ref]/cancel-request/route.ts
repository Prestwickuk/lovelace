import { NextRequest, NextResponse } from "next/server";
import { handlePortalBffError, parseJsonObject, PRIVATE_NO_STORE_HEADERS } from "@/lib/bff-response";
import { getPropertySlug } from "@/lib/lovelace-api";
import {
  assertNoQuerySecrets,
  assertSafeBookingRef,
  assertSameOriginMutation,
  callLovelacePortal,
  enforcePortalBffRateLimit,
} from "@/lib/portal-bff";
import type { PortalBookingDetail } from "@/lib/types";

export async function POST(
  req: NextRequest,
  context: { params: Promise<{ ref: string }> },
) {
  const limited = enforcePortalBffRateLimit(req, "portal-booking-cancel", 15);
  if (limited) return limited;

  try {
    assertNoQuerySecrets(req);
    assertSameOriginMutation(req);
    const { ref } = await context.params;
    const cleanRef = assertSafeBookingRef(ref);
    const body = await parseJsonObject<{ note?: string }>(req);
    const res = await callLovelacePortal<{ booking: PortalBookingDetail }>(
      req,
      `/bookings/${encodeURIComponent(cleanRef)}/cancel-request`,
      {
        method: "POST",
        body: JSON.stringify({
          property_slug: getPropertySlug(),
          note: body.note || "",
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
