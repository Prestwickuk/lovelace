import { NextRequest, NextResponse } from "next/server";
import { handlePortalBffError, PRIVATE_NO_STORE_HEADERS } from "@/lib/bff-response";
import { getPropertySlug } from "@/lib/lovelace-api";
import {
  assertNoQuerySecrets,
  assertSameOriginMutation,
  callLovelacePortal,
  enforcePortalBffRateLimit,
  setPortalSessionCookie,
} from "@/lib/portal-bff";
import type { PortalSessionView } from "@/lib/types";

export async function POST(req: NextRequest) {
  const limited = enforcePortalBffRateLimit(req, "portal-auth-booking-token", 15);
  if (limited) return limited;

  try {
    assertNoQuerySecrets(req);
    assertSameOriginMutation(req);
    const body = (await req.json()) as {
      booking_reference?: string;
      confirmation_token?: string;
    };

    const upstream = await callLovelacePortal<{
      session_token: string;
      session: PortalSessionView;
    }>(
      req,
      "/auth/booking-token",
      {
        method: "POST",
        body: JSON.stringify({
          property_slug: getPropertySlug(),
          booking_reference: body.booking_reference || "",
          confirmation_token: body.confirmation_token || "",
        }),
      },
      { requireSession: false, requireCsrf: false },
    );

    const res = NextResponse.json(
      { session: upstream.session },
      { status: 200, headers: PRIVATE_NO_STORE_HEADERS },
    );
    setPortalSessionCookie(res, upstream.session_token, upstream.session.scope_type);
    return res;
  } catch (err) {
    return handlePortalBffError(err);
  }
}
