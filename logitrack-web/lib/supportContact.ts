/**
 * The GORATECH support contact shown on `/app/support` (UI3C, #143). It comes from `NEXT_PUBLIC_SUPPORT_EMAIL`
 * (developer-spec.md §16.1, web-public: inlined at build like `NEXT_PUBLIC_GOOGLE_OIDC_CLIENT_ID`). No address is
 * written into the code: while the owner has not set one, the page shows no contact block at all.
 */
/** A plain address only: a safe local part and host labels, so nothing can add `?bcc=`, `#` or a path to the link. */
const EMAIL_RX = /^[A-Za-z0-9._+-]+@[A-Za-z0-9-]+(?:\.[A-Za-z0-9-]+)+$/;

/** The configured support address, or null when it is unset or not a plain address. */
export function supportEmail(raw: string | undefined = process.env.NEXT_PUBLIC_SUPPORT_EMAIL): string | null {
    const value = (raw ?? "").trim();
    return EMAIL_RX.test(value) ? value : null;
}

/** A `mailto:` link to the support address with the subject in the reader's language. */
export function supportMailto(email: string, subject: string): string {
    return `mailto:${email}?subject=${encodeURIComponent(subject)}`;
}
