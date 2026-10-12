/**
 * CSV formula injection (OWASP "CSV Injection"): a spreadsheet reads a cell whose text starts with `=`, `+`, `-`,
 * `@`, a tab or a CR as a formula (`=HYPERLINK(...)`, `=IMPORTXML(...)` send other rows to a host the text names).
 * Every CSV export of text other people typed (driver names, seal and partner codes, custom stops, audit summaries)
 * prefixes such a cell with `'`, so it opens as the text it is. Numbers are not text: a negative amount stays a number.
 */
const FORMULA_START = /^[=+\-@\t\r]/;

/** The text of a CSV cell with a formula-leading value neutralised by a leading `'`. */
export function neutraliseCsvFormula(text: string): string {
    return FORMULA_START.test(text) ? `'${text}` : text;
}
