import * as z from "zod";

// Helper to validate Thai ID / Tax ID (13 digits)
const validateThaiID = (id: string) => {
    if (!id || id.length !== 13 || !/^\d{13}$/.test(id)) return false;

    let sum = 0;
    for (let i = 0; i < 12; i++) {
        sum += parseInt(id.charAt(i)) * (13 - i);
    }
    const check = (11 - (sum % 11)) % 10;
    return check === parseInt(id.charAt(12));
};

export const subcontractorSchema = z.object({
    name: z.string({ error: "subcontractors.validation.nameRequired" }).trim().min(1, "subcontractors.validation.nameRequired"),
    type: z.enum(["individual", "company"], { error: "subcontractors.validation.type" }).default("individual"),

    // Individual specific
    idCardNumber: z.string().optional(),

    // Company specific
    taxId: z.string().optional(),

    contactPerson: z.string({ error: "subcontractors.validation.contactRequired" }).trim().min(1, "subcontractors.validation.contactRequired"),
    phone: z.string({ error: "subcontractors.validation.phoneRequired" }).trim().min(1, "subcontractors.validation.phoneRequired"),
    email: z.string().email("subcontractors.validation.email").optional().or(z.literal("")),
    website: z.string().url("subcontractors.validation.website").optional().or(z.literal("")),
    address: z.string().optional(),

    // Contact Details Extra
    designation: z.string().optional(),

    // Fleet & Service
    fleetSize: z.coerce.number({ error: "subcontractors.validation.fleetSize" })
        .int("subcontractors.validation.fleetSize").min(0, "subcontractors.validation.fleetSize")
        .max(2147483647, "subcontractors.validation.fleetSize").default(0),
    dispatchCenter: z.string().optional(),
    serviceRegions: z.array(z.string()).default([]),
    vehicleTypes: z.array(z.string()).default([]),

    status: z.enum(["active", "pending", "suspended"], { error: "subcontractors.validation.status" }).default("active"),

    documents: z.array(z.string()).optional(),

    // LINE group receiving check-in and job-complete notifications (blank disables them).
    lineGroupId: z.string().optional(),

    // Billing date basis (ADR 0027/0028) — when this partner is billed directly, which date decides
    // the billing period + rate round: "delivered" (default) = delivery instant; "plan" = the plan
    // date on the task. Mirrors customers.billingDateBasis; billing reads customers first, then here.
    billingDateBasis: z.enum(["delivered", "plan"], { error: "subcontractors.validation.billingDateBasis" }).optional(),
}).refine((data) => {
    if (data.type === "individual") {
        if (!data.idCardNumber) return false;
        return validateThaiID(data.idCardNumber);
    }
    return true;
}, {
    message: "subcontractors.validation.idCardNumber",
    path: ["idCardNumber"],
}).refine((data) => {
    if (data.type === "company") {
        if (!data.taxId) return false;
        return validateThaiID(data.taxId);
    }
    return true;
}, {
    message: "subcontractors.validation.taxId",
    path: ["taxId"],
});

export type SubcontractorFormValues = z.input<typeof subcontractorSchema>;
export type SubcontractorValidatedData = z.infer<typeof subcontractorSchema>;

export const subcontractorDefaultValues: SubcontractorFormValues = {
    name: "",
    type: "individual",
    idCardNumber: "",
    taxId: "",
    contactPerson: "",
    phone: "",
    email: "",
    website: "",
    address: "",
    designation: "",
    fleetSize: 0,
    dispatchCenter: "",
    serviceRegions: [],
    vehicleTypes: [],
    status: "active",
    documents: [],
    lineGroupId: "",
    billingDateBasis: "delivered",
};
