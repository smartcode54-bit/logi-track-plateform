import { clsx, type ClassValue } from "clsx"
import { extendTailwindMerge } from "tailwind-merge"

/**
 * tailwind-merge with the design tokens of app/globals.css (UI1, DESIGN.md): the layering scale is a z-index group
 * and the named spacing steps are spacing, so `cn("z-raised", "z-0")` or `cn("px-6", "p-gutter")` keeps the later class.
 */
const twMerge = extendTailwindMerge({
  extend: {
    classGroups: { z: [{ z: ["base", "raised", "sticky", "modal", "popover", "toast", "tooltip"] }] },
    theme: { spacing: ["gutter", "stack", "section"] },
  },
})

export function cn(...inputs: ClassValue[]) {
  return twMerge(clsx(inputs))
}

/**
 * Format license plate to display format: 12กข-4567
 * Format: [numbers][letters at positions 2-3][numbers]
 * Thai license plate pattern: 1-2 digits, 2 Thai letters, 4-5 digits
 * @param plate - Raw license plate string
 * @returns Formatted license plate with hyphen
 */
export function formatLicensePlate(plate: string): string {
  if (!plate) return "";

  // Remove all spaces and hyphens
  const cleaned = plate.replace(/[\s-]/g, "");

  // 1. Format: 1กก-1234 (7 chars: 1 digit + 2 letters + numbers)
  const patternType1 = /^([0-9][ก-ฮ]{2})([0-9]+)$/;
  const match1 = cleaned.match(patternType1);
  if (match1) {
    return `${match1[1]}-${match1[2]}`;
  }

  // 2. Format: กก-1234 (6 chars: 2 letters + numbers)
  const patternType2 = /^([ก-ฮ]{2})([0-9]+)$/;
  const match2 = cleaned.match(patternType2);
  if (match2) {
    return `${match2[1]}-${match2[2]}`;
  }

  // If already has hyphen, return as is
  if (plate.includes("-")) {
    return plate;
  }

  return cleaned;
}

/**
 * True if URL points to a PDF (handles Firebase Storage URLs that end with ?alt=media&token=...).
 * Do not use string.endsWith(".pdf") on full URLs.
 */
export function isPdfUrl(url: string): boolean {
  if (!url) return false;
  const noQueryOrHash = url.split("?")[0].split("#")[0];
  try {
    return decodeURIComponent(noQueryOrHash).toLowerCase().endsWith(".pdf");
  } catch {
    return noQueryOrHash.toLowerCase().endsWith(".pdf");
  }
}
