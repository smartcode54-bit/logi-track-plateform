/**
 * The avatar letter of a name (UI3): its first letter or digit. A Thai leading vowel (เ แ โ ใ ไ, U+0E40-U+0E44) is
 * written before the consonant it is spoken after, so it is skipped: "เกศินี" shows ก, not เ. "?" when the name
 * holds no letter.
 */
const THAI_LEADING_VOWEL = /[เ-ไ]/;
const LETTER_OR_DIGIT = /[\p{L}\p{N}]/u;

export function initialOf(name: string): string {
    for (const ch of name.trim()) {
        if (THAI_LEADING_VOWEL.test(ch)) continue;
        if (LETTER_OR_DIGIT.test(ch)) return ch.toLocaleUpperCase();
    }
    return "?";
}
