import { describe, expect, it } from "vitest";
import {
  SlidingFixedWindowLimiter,
  isIpInCidr,
  resolveTrustedClientIp,
} from "./rate-limit";

describe("BFF rate limiting & trusted proxy extraction", () => {
  it("ignores spoofed X-Forwarded-For when trustProxy is disabled", () => {
    const ip = resolveTrustedClientIp({
      xForwardedFor: "203.0.113.99",
      xRealIp: "198.51.100.44",
      remoteAddr: "192.0.2.10",
      trustProxy: false,
      trustedProxies: ["10.0.0.0/8"],
    });
    expect(ip).toBe("192.0.2.10");
  });

  it("ignores spoofed headers from untrusted remote peers even when trustProxy is enabled", () => {
    const ip = resolveTrustedClientIp({
      xForwardedFor: "198.51.100.77",
      remoteAddr: "203.0.113.5",
      trustProxy: true,
      trustedProxies: ["10.0.0.0/8", "172.16.0.0/12"],
    });
    expect(ip).toBe("203.0.113.5");
  });

  it("walks X-Forwarded-For right-to-left skipping trusted proxy CIDRs", () => {
    const ip = resolveTrustedClientIp({
      xForwardedFor: "198.51.100.1, 203.0.113.42, 10.1.2.3",
      remoteAddr: "172.18.0.2",
      trustProxy: true,
      trustedProxies: ["10.0.0.0/8", "172.16.0.0/12"],
    });
    expect(ip).toBe("203.0.113.42");
  });

  it("matches IPv4 and IPv6 CIDRs accurately", () => {
    expect(isIpInCidr("172.19.0.4", "172.16.0.0/12")).toBe(true);
    expect(isIpInCidr("172.32.0.4", "172.16.0.0/12")).toBe(false);
    expect(isIpInCidr("2001:db8::5", "2001:db8::/32")).toBe(true);
  });

  it("enforces fixed window limits and isolates distinct clients", () => {
    const limiter = new SlidingFixedWindowLimiter(2, 60_000);
    const t0 = 1_700_000_000_000;
    expect(limiter.allow("client-a", t0).allowed).toBe(true);
    expect(limiter.allow("client-a", t0 + 100).allowed).toBe(true);
    const blocked = limiter.allow("client-a", t0 + 200);
    expect(blocked.allowed).toBe(false);
    expect(blocked.retryAfterSeconds).toBeGreaterThan(0);

    // Separate client still allowed
    expect(limiter.allow("client-b", t0 + 200).allowed).toBe(true);

    // Resets after window elapses
    expect(limiter.allow("client-a", t0 + 60_001).allowed).toBe(true);
  });
});
