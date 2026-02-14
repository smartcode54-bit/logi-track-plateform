import { clsx, type ClassValue } from "clsx"
import { twMerge } from "tailwind-merge"

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
