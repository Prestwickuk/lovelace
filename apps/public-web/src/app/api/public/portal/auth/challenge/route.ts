import { NextRequest, NextResponse } from "next/server";
import { handlePortalBffError, PRIVATE_NO_STORE_HEADERS } from "@/lib/bff-response";
import { getPropertySlug } from "@/lib/lovelace-api";
import {
  assertNoQuerySecrets,
  assertSameOriginMutation,
  callLovelacePortal,
  enforcePortalBffRateLimit,
  setPortalNonceCookie,
} from "@/lib/portal-bff";

export async function POST(req: NextRequest) {
  const limited = enforcePortalBffRateLimit(req, "portal-auth-challenge", 12);
  if (limited) return limited;

  try {
    assertNoQuerySecrets(req);
    assertSameOriginMutation(req);
    const body = (await req.json()) as {
      email?: string;
      purpose?: string;
      booking_reference?: string;
    };
    const upstream = await callLovelacePortal<{
      accepted: boolean;
      browser_nonce?: string;
      expires_in_seconds: number;
    }>(
      req,
      "/auth/challenge",
      {
        method: "POST",
        body: JSON.stringify({
          property_slug: getPropertySlug(),
          email: body.email || "",
          purpose: "LOGIN",
          booking_reference: body.booking_reference || "",
        }),
      },
      { requireSession: false, requireCsrf: false },
    );

    const res = NextResponse.json(
      {
        accepted: true,
        expires_in_seconds: upstream.expires_in_seconds,
      },
      { status: 202, headers: PRIVATE_NO_STORE_HEADERS },
    );
    if (upstream.browser_nonce) {
      setPortalNonceCookie(res, upstream.browser_nonce);
    }
    return res;
  } catch (err) {
    return handlePortalBffError(err);
  }
}
