// @vitest-environment node
import { readFileSync } from "node:fs";
import path from "node:path";
import { describe, expect, it } from "vitest";

type RGB = [number, number, number];
const css = readFileSync(path.join(__dirname, "../app/globals.css"), "utf8").replace(/\/\*[\s\S]*?\*\//g, "");
const badgeSource = readFileSync(path.join(__dirname, "../components/ui/badge.tsx"), "utf8");

/** CSS Color 4 OKLCH -> OKLab -> linear sRGB -> display sRGB, clipped to the display gamut. */
function oklchToSrgb(value: string): RGB {
    const match = value.match(/^oklch\(([\d.]+) ([\d.]+) ([\d.]+)\)$/);
    if (!match) throw new Error(`Expected an opaque OKLCH token: ${value}`);
    const [, lightness, chroma, hue] = match;
    const L = Number(lightness), C = Number(chroma), h = Number(hue) * Math.PI / 180;
    const a = C * Math.cos(h), b = C * Math.sin(h);
    const l = (L + 0.3963377774 * a + 0.2158037573 * b) ** 3;
    const m = (L - 0.1055613458 * a - 0.0638541728 * b) ** 3;
    const s = (L - 0.0894841775 * a - 1.2914855480 * b) ** 3;
    return [
        4.0767416621 * l - 3.3077115913 * m + 0.2309699292 * s,
        -1.2684380046 * l + 2.6097574011 * m - 0.3413193965 * s,
        -0.0041960863 * l - 0.7034186147 * m + 1.7076147010 * s,
    ].map((linear) => {
        const clipped = Math.max(0, Math.min(1, linear));
        return clipped <= 0.0031308 ? 12.92 * clipped : 1.055 * clipped ** (1 / 2.4) - 0.055;
    }) as RGB;
}

function luminance(rgb: RGB): number {
    const [r, g, b] = rgb.map((v) => v <= 0.04045 ? v / 12.92 : ((v + 0.055) / 1.055) ** 2.4);
    return 0.2126 * r + 0.7152 * g + 0.0722 * b;
}

function contrast(foreground: RGB, background: RGB): number {
    const a = luminance(foreground), b = luminance(background);
    return (Math.max(a, b) + 0.05) / (Math.min(a, b) + 0.05);
}

function tokens(scope: ":root" | ".dark") {
    const start = css.indexOf(`${scope} {`);
    if (start < 0) throw new Error(`Missing theme ${scope}`);
    const body = css.slice(start, css.indexOf("}", start));
    const values = Object.fromEntries([...body.matchAll(/--([\w-]+):\s*([^;]+);/g)].map((m) => [m[1], m[2].trim()]));
    return (name: string) => oklchToSrgb(values[name] ?? `Missing --${name} in ${scope}`);
}

describe("status token contrast (WCAG AA)", () => {
    it("keeps asChild success and warning hover fills opaque", () => {
        for (const tone of ["success", "warning"] as const) {
            const variant = badgeSource.match(new RegExp(`${tone}: "([^"]+)"`))?.[1];
            expect(variant).toContain(`bg-${tone}`);
            expect(variant).not.toMatch(new RegExp(`bg-${tone}\\/\\d+`));
        }
    });

    it("checks the conversion and WCAG reference values", () => {
        expect(oklchToSrgb("oklch(0 0 0)")).toEqual([0, 0, 0]);
        oklchToSrgb("oklch(1 0 0)").forEach((v) => expect(v).toBeCloseTo(1, 6));
        expect(oklchToSrgb("oklch(0.6279553606 0.2576833077 29.2338851923)")[0]).toBeCloseTo(1, 6);
        expect(luminance([1, 0, 0])).toBe(0.2126);
        expect(contrast([0, 0, 0], [1, 1, 1])).toBe(21);
    });

    for (const theme of [":root", ".dark"] as const) {
        const token = tokens(theme);
        for (const tone of ["success", "warning", "secondary"] as const) {
            const ratio = contrast(token(`${tone}-foreground`), token(tone));
            it(`${theme} filled ${tone} Badge: ${ratio.toFixed(2)}:1`, () => {
                expect(ratio).toBeGreaterThanOrEqual(4.5);
            });
        }
        for (const tone of ["success", "warning", "destructive", "primary"] as const) {
            for (const surface of ["card", "background"] as const) {
                const ratio = contrast(token(tone), token(surface));
                it(`${theme} text-${tone} on ${surface}: ${ratio.toFixed(2)}:1`, () => {
                    expect(ratio).toBeGreaterThanOrEqual(4.5);
                });
            }
        }
    }
    // Existing destructive Badge keeps dark:bg-destructive/60: about 5.7:1 on dark card
    // (the unmodified opaque destructive pair is about 2.8:1). Do not copy it for new variants.
});
