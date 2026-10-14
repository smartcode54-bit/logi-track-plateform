// UI3 (#143): the avatar letter of the user menu and the public navigation.
import { describe, expect, it } from "vitest";

import { initialOf } from "./initials";

describe("initialOf", () => {
    it("takes the first letter, upper case for Latin", () => {
        expect(initialOf("ann smith")).toBe("A");
        expect(initialOf("  Somchai")).toBe("S");
        expect(initialOf("ops@example.test")).toBe("O");
    });

    it("skips a Thai leading vowel (เ แ โ ใ ไ) to the consonant it is spoken after", () => {
        expect(initialOf("เกศินี")).toBe("ก");
        expect(initialOf("แสงดาว")).toBe("ส");
        expect(initialOf("โชคดี")).toBe("ช");
        expect(initialOf("ใจดี")).toBe("จ");
        expect(initialOf("ไพโรจน์")).toBe("พ");
        expect(initialOf("สมชาย")).toBe("ส");
    });

    it("skips punctuation and falls back to ? without a letter or digit", () => {
        expect(initialOf("(TTP) Transport")).toBe("T");
        expect(initialOf("7-Eleven")).toBe("7");
        expect(initialOf("")).toBe("?");
        expect(initialOf("— ·")).toBe("?");
    });
});
