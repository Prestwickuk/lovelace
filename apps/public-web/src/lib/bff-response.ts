import "server-only";

import { NextResponse } from "next/server";
import { LovelaceApiError } from "./lovelace-api";

export const PRIVATE_NO_STORE_HEADERS: Record<string, string> = {
  "Cache-Control": "no-store, no-cache, must-revalidate, private, max-age=0",
  Pragma: "no-cache",
  Expires: "0",
  "Referrer-Policy": "no-referrer",
  "X-Content-Type-Options": "nosniff",
  "X-Robots-Tag": "noindex, nofollow",
};

export function handleBffError(err: unknown, extraHeaders?: Record<string, string>) {
  if (err instanceof LovelaceApiError) {
    return NextResponse.json(
      {
        error: {
          code: err.code,
          message: err.message,
          fields: err.fields,
        },
      },
      { status: err.status, headers: extraHeaders },
    );
  }
  return NextResponse.json(
    {
      error: {
        code: "upstream_unavailable",
        message: "Booking service temporarily unavailable. Please try again.",
      },
    },
    { status: 502, headers: extraHeaders },
  );
}

export function handlePortalBffError(err: unknown) {
  return handleBffError(err, PRIVATE_NO_STORE_HEADERS);
}
