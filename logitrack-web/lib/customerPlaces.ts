/**
 * One job = one customer. The admin picks the billing customer first, and the job's route — source,
 * destination and every delivery stop — is picked only from places linked to that customer
 * (`hubs.linkedCustomerId`, which the Sources page requires on every hub and SOC). A place with no
 * link is left out on purpose rather than shown to everyone: link it on the Sources page.
 *
 * A place carries one customer link, but a customer may run on another customer's places
 * (`customers.usePlacesOfCustomerId` — e.g. JWT on TTP's SPX points). Such a customer's "place owners"
 * are itself plus that one customer; the borrowing is one hop, never followed further. The job is
 * still the borrower's: its route fields are stamped with the job's customer (`linkCustomerIdForPlace`).
 */
import { normalizeSocIdToKey } from "@/validate/taskSchema";

/** One customer id or the owner list from `placeOwnerIds`. */
type CustomerIds = string | string[] | undefined;

function idList(customerIds: CustomerIds): string[] {
    const list = Array.isArray(customerIds) ? customerIds : [customerIds];
    return list.map((id) => (id ?? "").trim()).filter(Boolean);
}

/**
 * The customers whose places a job for `customerId` may use: the customer first, then the customer
 * it borrows places from (if any). Empty until a customer is chosen.
 */
export function placeOwnerIds(
    customerId: string | undefined,
    lookup: (id: string) => { usePlacesOfCustomerId?: string } | undefined
): string[] {
    const id = (customerId ?? "").trim();
    if (!id) return [];
    const borrowed = (lookup(id)?.usePlacesOfCustomerId ?? "").trim();
    return borrowed && borrowed !== id ? [id, borrowed] : [id];
}

export function isLinkedToCustomer(
    place: { linkedCustomerId?: unknown } | undefined,
    customerIds: CustomerIds
): boolean {
    const ids = idList(customerIds);
    if (!place || ids.length === 0) return false;
    return ids.includes(String(place.linkedCustomerId ?? "").trim());
}

/** The places linked to any of `customerIds`; none until a customer is chosen. */
export function placesOfCustomer<T extends { linkedCustomerId?: unknown }>(
    places: T[],
    customerIds: CustomerIds
): T[] {
    return places.filter((place) => isLinkedToCustomer(place, customerIds));
}

/**
 * The customer a job's route field (`sourceHubLinkedCustomerId`, `destinationLinkedCustomerId`, a
 * stop's link) is stamped with. Customer-scoped task lists match on these, so a place the job's
 * customer may use — its own or borrowed — is stamped with the job's customer (`ownerIds[0]`), never
 * the place's: a JWT job at a TTP point stays JWT's. A place outside the owners (a legacy job) keeps
 * its own link; an unlinked place gives none.
 */
export function linkCustomerIdForPlace(
    place: { linkedCustomerId?: unknown } | undefined,
    ownerIds: string[]
): string | undefined {
    const linked = String(place?.linkedCustomerId ?? "").trim();
    if (!linked) return undefined;
    return ownerIds.includes(linked) ? ownerIds[0] : linked;
}

/** Lowercased, with spaces, dashes and underscores dropped — hub codes are typed as "ABYAI-B", "ABYAI - B", "ABYAI B". */
function searchKey(text: string): string {
    return text.toLowerCase().replace(/[\s\-_]+/g, "");
}

/** Search a place picker by code or name; an empty query matches everything. */
export function placeMatchesQuery(place: { code: string; name?: string }, query: string): boolean {
    const q = searchKey(query);
    if (!q) return true;
    return searchKey(place.code).includes(q) || searchKey(place.name ?? "").includes(q);
}

/** Same place code — exact, or the same SOC once a legacy key ("SOCE", "SOCE (…)") is normalized. */
export function samePlaceCode(a: string | undefined, b: string | undefined): boolean {
    const x = (a ?? "").trim();
    const y = (b ?? "").trim();
    if (!x || !y) return false;
    return x === y || normalizeSocIdToKey(x) === normalizeSocIdToKey(y);
}
