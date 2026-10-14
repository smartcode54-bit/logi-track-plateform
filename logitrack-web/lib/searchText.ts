/**
 * Text matching of the searchable select (UI2, issue #142): case- and diacritic-insensitive, for Thai
 * and English.
 *
 * - Compatibility forms fold (NFKD: full-width "ＡＢ" = "AB", sara am "ำ" = nikhahit + sara aa), and Latin
 *   accents fold away (the combining marks U+0300-U+036F are dropped): "Café" = "cafe".
 * - Thai tone marks and the other marks above a consonant that change neither the letters nor the
 *   order fold away (U+0E47 maitaikhu, U+0E48-U+0E4B tone marks, U+0E4C thanthakhat, U+0E4D nikhahit,
 *   U+0E4E yamakkan): "ห้วยขวาง" matches "หวยขวาง", so a missing or wrong tone mark still finds the row.
 *   Vowel signs stay: they change the word.
 * - Thai digits read as Arabic digits: "๑๒" = "12".
 * - Case folds with toLocaleLowerCase, and runs of white space collapse to one space.
 *
 * A query matches when every one of its space-separated words is somewhere in the text (any order),
 * so "บางปู j&t" finds "J&T EXPRESS บางปู".
 */

const LATIN_MARKS = /[\u0300-\u036f]/g;
const THAI_MARKS = /[\u0e47-\u0e4e]/g;
const THAI_DIGITS = /[\u0e50-\u0e59]/g;
const SPACES = /\s+/g;

/** The folded form of `s` that searches compare (see the file comment). */
export function foldSearchText(s: string | null | undefined): string {
    if (!s) return "";
    return s
        .normalize("NFKD")
        .replace(LATIN_MARKS, "")
        .replace(THAI_MARKS, "")
        .replace(THAI_DIGITS, (d) => String(d.charCodeAt(0) - 0x0e50))
        .normalize("NFC")
        .toLocaleLowerCase()
        .replace(SPACES, " ")
        .trim();
}

/** The folded words of a query; none for a blank one. */
export function searchTerms(query: string | null | undefined): string[] {
    const folded = foldSearchText(query);
    return folded ? folded.split(" ") : [];
}

/** Whether every term is in `foldedText` (an already folded haystack). */
export function matchesTerms(foldedText: string, terms: readonly string[]): boolean {
    for (const term of terms) {
        if (!foldedText.includes(term)) return false;
    }
    return true;
}

/**
 * The items whose text matches `query`, in their order, except that items with a word starting with
 * the first term come first (typing a code's start finds that code before rows that merely contain it).
 * `getText` gives an item's searchable text; it is folded once per call.
 */
export function filterBySearch<T>(items: readonly T[], query: string | null | undefined, getText: (item: T) => string): T[] {
    const terms = searchTerms(query);
    if (terms.length === 0) return items.slice();
    const first = terms[0];
    const leading: T[] = [];
    const rest: T[] = [];
    for (const item of items) {
        const text = foldSearchText(getText(item));
        if (!matchesTerms(text, terms)) continue;
        if (text.startsWith(first) || text.includes(` ${first}`)) leading.push(item);
        else rest.push(item);
    }
    return leading.concat(rest);
}
