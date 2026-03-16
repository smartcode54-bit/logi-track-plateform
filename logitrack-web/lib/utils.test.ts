import { describe, it, expect } from "vitest";
import { cn, formatLicensePlate } from "./utils";

describe("utils", () => {
  describe("cn", () => {
    it("merges class names", () => {
      expect(cn("foo", "bar")).toBe("foo bar");
    });

    it("handles conditional classes", () => {
      expect(cn("base", false && "hidden", true && "visible")).toBe("base visible");
    });

    it("handles tailwind merge (later class wins)", () => {
      expect(cn("p-4", "p-2")).toBe("p-2");
    });

    it("handles empty and undefined", () => {
      expect(cn("a", undefined, null, "", "b")).toBe("a b");
    });
  });

  describe("formatLicensePlate", () => {
    it("returns empty string for empty input", () => {
      expect(formatLicensePlate("")).toBe("");
    });

    it("formats type 1 pattern: 1 digit + 2 Thai letters + numbers", () => {
      expect(formatLicensePlate("1กข1234")).toBe("1กข-1234");
      expect(formatLicensePlate("1กก12345")).toBe("1กก-12345");
    });

    it("formats type 2 pattern: 2 Thai letters + numbers", () => {
      expect(formatLicensePlate("กข1234")).toBe("กข-1234");
    });

    it("removes spaces and hyphens before formatting", () => {
      expect(formatLicensePlate("1 ก ข 1234")).toBe("1กข-1234");
      expect(formatLicensePlate("1กข-1234")).toBe("1กข-1234");
    });

    it("returns cleaned string when no pattern matches", () => {
      expect(formatLicensePlate("ABC123")).toBe("ABC123");
    });
  });
});
