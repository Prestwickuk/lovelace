import { NextRequest, NextResponse } from "next/server";
import { handlePortalBffError, PRIVATE_NO_STORE_HEADERS } from "@/lib/bff-response";
import { getPropertySlug } from "@/lib/lovelace-api";
import {
  assertNoQuerySecrets,
  assertSafeBookingRef,
  assertSameOriginMutation,
  callLovelacePortal,
  enforcePortalBffRateLimit,
} from "@/lib/portal-bff";
import type { PortalBookingDetail } from "@/lib/types";

export async function PATCH(
  req: NextRequest,
  context: { params: Promise<{ ref: string }> },
) {
  const limited = enforcePortalBffRateLimit(req, "portal-booking-arrival", 20);
  if (limited) return limited;

  try {
    assertNoQuerySecrets(req);
    assertSameOriginMutation(req);
    const { ref } = await context.params;
    const cleanRef = assertSafeBookingRef(ref);
    const body = (await req.json()) as {
      estimated_arrival_time?: string;
      guest_arrival_notes?: string;
    };
    const res = await callLovelacePortal<{ booking: PortalBookingDetail }>(
      req,
      `/bookings/${encodeURIComponent(cleanRef)}/arrival`,
      {
        method: "PATCH",
        body: JSON.stringify({
          property_slug: getPropertySlug(),
          estimated_arrival_time: body.estimated_arrival_time || "",
          guest_arrival_notes: body.guest_arrival_notes || "",
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
