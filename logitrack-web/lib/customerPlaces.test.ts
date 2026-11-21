import { describe, it, expect } from "vitest";
import {
    isLinkedToCustomer,
    linkCustomerIdForPlace,
    placeMatchesQuery,
    placeOwnerIds,
    placesOfCustomer,
    samePlaceCode,
} from "./customerPlaces";

const places = [
    { code: "SPK890103", linkedCustomerId: "cust-jt" },
    { code: "SPK890174", linkedCustomerId: " cust-jt " },
    { code: "BKK-SPX", linkedCustomerId: "cust-spx" },
    { code: "DRIVER-ADDED", linkedCustomerId: "" },
    { code: "LEGACY" },
];

describe("isLinkedToCustomer", () => {
    it("matches the place's linked customer, ignoring stray whitespace", () => {
        expect(isLinkedToCustomer(places[0], "cust-jt")).toBe(true);
        expect(isLinkedToCustomer(places[1], "cust-jt")).toBe(true);
    });

    it("rejects another customer's place", () => {
        expect(isLinkedToCustomer(places[2], "cust-jt")).toBe(false);
    });

    it("rejects a place with no link — never treated as everyone's", () => {
        expect(isLinkedToCustomer(places[3], "cust-jt")).toBe(false);
        expect(isLinkedToCustomer(places[4], "cust-jt")).toBe(false);
    });

    it("rejects everything until a customer is chosen", () => {
        expect(isLinkedToCustomer(places[0], "")).toBe(false);
        expect(isLinkedToCustomer(places[0], undefined)).toBe(false);
        expect(isLinkedToCustomer(undefined, "cust-jt")).toBe(false);
    });
});

describe("placesOfCustomer", () => {
    it("keeps only the chosen customer's places, in order", () => {
        expect(placesOfCustomer(places, "cust-jt").map((p) => p.code)).toEqual(["SPK890103", "SPK890174"]);
    });

    it("is empty with no customer, even though unlinked places exist", () => {
        expect(placesOfCustomer(places, "")).toEqual([]);
    });
});

describe("samePlaceCode", () => {
    it("matches identical codes", () => {
        expect(samePlaceCode("SPK890103", " SPK890103 ")).toBe(true);
    });

    it("matches a legacy SOC key to its normalized form", () => {
        expect(samePlaceCode("SOCE (บางนา)", "SOCE")).toBe(true);
    });

    it("does not match different hubs or empty values", () => {
        expect(samePlaceCode("SPK890103", "SPK890174")).toBe(false);
        expect(samePlaceCode("", "")).toBe(false);
        expect(samePlaceCode(undefined, "SPK890103")).toBe(false);
    });
});

describe("placeMatchesQuery", () => {
    const hub = { code: "ABYAI - B", name: "บางใหญ่ B" };

    it("matches everything for an empty or blank query", () => {
        expect(placeMatchesQuery(hub, "")).toBe(true);
        expect(placeMatchesQuery(hub, "   ")).toBe(true);
    });

    it("matches the code case-insensitively, ignoring spaces and dashes", () => {
        expect(placeMatchesQuery(hub, "abyai-b")).toBe(true);
        expect(placeMatchesQuery(hub, "ABYAI B")).toBe(true);
        expect(placeMatchesQuery(hub, "yaib")).toBe(true);
    });

    it("matches the Thai name", () => {
        expect(placeMatchesQuery(hub, "บางใหญ่")).toBe(true);
        expect(placeMatchesQuery({ code: "SOCN", name: "ศูนย์คัดแยกสินค้าวังน้อย" }, "วังน้อย")).toBe(true);
    });

    it("rejects a query found in neither code nor name", () => {
        expect(placeMatchesQuery(hub, "บางนา")).toBe(false);
        expect(placeMatchesQuery({ code: "SOCE" }, "socn")).toBe(false);
    });
});

describe("placeOwnerIds", () => {
    const customers: Record<string, { usePlacesOfCustomerId?: string }> = {
        jwt: { usePlacesOfCustomerId: "ttp" },
        ttp: {},
        self: { usePlacesOfCustomerId: "self" },
        blank: { usePlacesOfCustomerId: "  " },
        chain: { usePlacesOfCustomerId: "jwt" },
    };
    const lookup = (id: string) => customers[id];

    it("is the customer alone when it borrows nothing", () => {
        expect(placeOwnerIds("ttp", lookup)).toEqual(["ttp"]);
        expect(placeOwnerIds("blank", lookup)).toEqual(["blank"]);
        expect(placeOwnerIds("unknown", lookup)).toEqual(["unknown"]);
    });

    it("adds the customer it borrows places from, job customer first", () => {
        expect(placeOwnerIds("jwt", lookup)).toEqual(["jwt", "ttp"]);
    });

    it("never follows borrowing past one hop, nor borrows from itself", () => {
        expect(placeOwnerIds("chain", lookup)).toEqual(["chain", "jwt"]);
        expect(placeOwnerIds("self", lookup)).toEqual(["self"]);
    });

    it("is empty until a customer is chosen", () => {
        expect(placeOwnerIds("", lookup)).toEqual([]);
        expect(placeOwnerIds(undefined, lookup)).toEqual([]);
    });
});

describe("borrowed places", () => {
    const spxHub = { code: "ABYAI-B", linkedCustomerId: "ttp" };
    const cjHub = { code: "SPK890103", linkedCustomerId: "cjsf" };
    const jwtOwners = ["jwt", "ttp"];

    it("lists the lender's places for the borrower", () => {
        expect(isLinkedToCustomer(spxHub, jwtOwners)).toBe(true);
        expect(placesOfCustomer([spxHub, cjHub], jwtOwners)).toEqual([spxHub]);
    });

    it("stamps a borrowed place with the job's customer, not the place's", () => {
        expect(linkCustomerIdForPlace(spxHub, jwtOwners)).toBe("jwt");
        expect(linkCustomerIdForPlace(spxHub, ["ttp"])).toBe("ttp");
    });

    it("keeps a legacy place's own link and gives none for an unlinked place", () => {
        expect(linkCustomerIdForPlace(cjHub, jwtOwners)).toBe("cjsf");
        expect(linkCustomerIdForPlace({ linkedCustomerId: " " }, jwtOwners)).toBeUndefined();
        expect(linkCustomerIdForPlace(undefined, jwtOwners)).toBeUndefined();
    });
});
