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
    name: z.string().min(1, "Name is required"),
    type: z.enum(["individual", "company"]).default("individual"),

    // Individual specific
    idCardNumber: z.string().optional(),

    // Company specific
    taxId: z.string().optional(),

    contactPerson: z.string().min(1, "Contact person is required"),
    phone: z.string().min(1, "Phone is required"),
    email: z.string().email("Invalid email").optional().or(z.literal("")),
    website: z.string().url("Invalid URL").optional().or(z.literal("")),
    address: z.string().optional(),

    // Contact Details Extra
    designation: z.string().optional(),

    // Fleet & Service
    fleetSize: z.coerce.number().min(0).default(0),
    dispatchCenter: z.string().optional(),
    serviceRegions: z.array(z.string()).default([]),
    vehicleTypes: z.array(z.string()).default([]),

    status: z.enum(["active", "pending", "suspended"]).default("active"),

    documents: z.array(z.string()).optional(),
}).refine((data) => {
    if (data.type === "individual") {
        if (!data.idCardNumber) return false;
        return validateThaiID(data.idCardNumber);
    }
    return true;
}, {
    message: "โปรดกรอก เลขบัตรประจำตัวประชาชน/รหัสประจำตัวนิติบุคคล ของรถร่วมให้ถูกต้อง",
    path: ["idCardNumber"],
}).refine((data) => {
    if (data.type === "company") {
        if (!data.taxId) return false;
        return validateThaiID(data.taxId);
    }
    return true;
}, {
    message: "โปรดกรอก เลขประจำตัวผู้เสียภาษี/รหัสประจำตัวนิติบุคคล ของรถร่วมให้ถูกต้อง",
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
};
