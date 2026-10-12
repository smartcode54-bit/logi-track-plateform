/**
 * The landing pages' anonymous forms (R44, R77; T54R): the browser posts to the same-origin BFF route
 * handlers `POST /api/forms/waitlist` and `POST /api/forms/partner-interest` (lib/bff/forms.ts), which
 * forward to Go's internal `POST /v1/waitlist` / `POST /v1/partner-interest`. Nothing is written to
 * Firestore and no session is involved: the request carries no cookie (`credentials: "omit"`), and the
 * BFF would not forward one.
 *
 * Errors are `ApiError`s in Go's envelope (lib/apiError.ts): 422 `invalid_argument` with
 * `details.fields`, 429 `resource_exhausted` (5 posts per hour per visitor, one per day per email;
 * `RATE_LIMIT_PUBLIC_FORMS`), 403 for a cross-origin post. Both forms carry the hidden honeypot field
 * `website` (`HONEYPOT_FIELD`), which a person leaves empty; Go answers a filled one like any post and
 * stores nothing.
 */
import { apiErrorFromResponse, networkError } from "./apiError";

export const PUBLIC_FORM_PATHS = {
    waitlist: "/api/forms/waitlist",
    partnerInterest: "/api/forms/partner-interest",
} as const;

/** Go's honeypot field (`forms.HoneypotField`). */
export const HONEYPOT_FIELD = "website";

/** `POST /v1/waitlist` (Go `forms.WaitlistInput`): email and name required, countryCode defaults to +66. */
export interface WaitlistSubmission {
    email: string;
    name: string;
    countryCode: string;
    phone: string;
    website: string;
}

/** `POST /v1/partner-interest` (Go `forms.PartnerInterestInput`): companyName, contactName and email required. */
export interface PartnerInterestSubmission {
    companyName: string;
    contractNumber: string;
    contactName: string;
    email: string;
    serviceArea: string;
    fleetSize: string;
    vehicleType: string;
    website: string;
}

/** Posts one anonymous form; resolves on 2xx, throws an `ApiError` otherwise. */
export async function postPublicForm(path: (typeof PUBLIC_FORM_PATHS)[keyof typeof PUBLIC_FORM_PATHS], body: object): Promise<void> {
    let res: Response;
    try {
        res = await fetch(path, {
            method: "POST",
            headers: { Accept: "application/json", "Content-Type": "application/json" },
            body: JSON.stringify(body),
            credentials: "omit",
            cache: "no-store",
        });
    } catch (error) {
        throw networkError(error);
    }
    if (!res.ok) throw await apiErrorFromResponse(res);
}

export function submitWaitlist(input: WaitlistSubmission): Promise<void> {
    return postPublicForm(PUBLIC_FORM_PATHS.waitlist, input);
}

export function submitPartnerInterest(input: PartnerInterestSubmission): Promise<void> {
    return postPublicForm(PUBLIC_FORM_PATHS.partnerInterest, input);
}
