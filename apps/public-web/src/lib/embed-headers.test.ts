import { describe, expect, it } from "vitest";
import nextConfig from "../../next.config";

describe("Next.js framing and security header routing", () => {
  it("keeps non-embed routes DENY while allowing /embed/:path* framing without X-Frame-Options", async () => {
    const rules = await nextConfig.headers!();
    const globalRule = rules.find((r) => r.source.includes("(?!embed"));
    const embedWildcard = rules.find((r) => r.source === "/embed/:path*");

    expect(globalRule).toBeDefined();
    expect(embedWildcard).toBeDefined();

    const globalXfo = globalRule!.headers.find(
      (h) => h.key === "X-Frame-Options",
    );
    const globalCsp = globalRule!.headers.find(
      (h) => h.key === "Content-Security-Policy",
    );
    expect(globalXfo?.value).toBe("DENY");
    expect(globalCsp?.value).toContain("frame-ancestors 'none'");

    // Verify regex exclusion behavior on representative routes
    const nonEmbedRegex = new RegExp(`^${globalRule!.source}$`);
    expect(nonEmbedRegex.test("/")).toBe(true);
    expect(nonEmbedRegex.test("/stays")).toBe(true);
    expect(nonEmbedRegex.test("/book")).toBe(true);
    expect(nonEmbedRegex.test("/embedded-guide")).toBe(true);
    expect(nonEmbedRegex.test("/embed")).toBe(false);
    expect(nonEmbedRegex.test("/embed/tir-bach")).toBe(false);

    const embedXfo = embedWildcard!.headers.find(
      (h) => h.key === "X-Frame-Options",
    );
    const embedCsp = embedWildcard!.headers.find(
      (h) => h.key === "Content-Security-Policy",
    );
    expect(embedXfo).toBeUndefined();
    expect(embedCsp?.value).toContain("frame-ancestors https:");
  });
});
