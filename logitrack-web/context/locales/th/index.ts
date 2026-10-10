// Every Thai namespace in one dictionary. The order is fixed and matches the other language: a
// later namespace wins on a duplicate key. Loaded only through ../load.ts, never statically, so no
// page carries a dictionary in its initial JS (developer-spec.md §10.11, Appendix E §E.7 row 9).
// `accounting` and `driverMonitor` are not here: they load per route group (../routes.ts, TW4).
import type { Dictionary } from "../load";
import common from "./common";
import auth from "./auth";
import dashboard from "./dashboard";
import trucks from "./trucks";
import maintenance from "./maintenance";
import users from "./users";
import assignments from "./assignments";
import renewals from "./renewals";
import subcontractors from "./subcontractors";
import waitlist from "./waitlist";
import landing from "./landing";
import drivers from "./drivers";
import firstMile from "./firstMile";
import customers from "./customers";
import securityCenter from "./securityCenter";
import holidays from "./holidays";
import about from "./about";
import company from "./company";
import driverCompensation from "./driverCompensation";
import tenants from "./tenants";
import apiErrors from "./apiErrors";
import imagePreview from "./imagePreview";

const dictionary: Dictionary = {
    ...common,
    ...auth,
    ...dashboard,
    ...trucks,
    ...maintenance,
    ...users,
    ...assignments,
    ...renewals,
    ...subcontractors,
    ...waitlist,
    ...landing,
    ...drivers,
    ...firstMile,
    ...customers,
    ...securityCenter,
    ...holidays,
    ...about,
    ...company,
    ...driverCompensation,
    ...tenants,
    ...apiErrors,
    ...imagePreview,
};

export default dictionary;
