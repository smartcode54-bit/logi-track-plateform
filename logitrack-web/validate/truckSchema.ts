import * as z from "zod";

// Helper for optional numeric fields to handle empty strings
// Use union + transform instead of preprocess for better type safety
const optionalNumber = (min: number, max: number, label: string) =>
    z.union([z.string(), z.number(), z.undefined()])
        .transform((val) => {
            if (val === "" || val === null || val === undefined) return undefined;
            if (typeof val === "string") {
                const num = parseFloat(val);
                return isNaN(num) ? undefined : num;
            }
            return typeof val === "number" ? val : undefined;
        })
        .pipe(
            z.number()
                .min(min, `${label} must be at least ${min}`)
                .max(max, `${label} cannot exceed ${max}`)
                .refine((val) => val >= 0, `${label} cannot be negative`)
                .optional()
        );

// Truck form validation schema
export const truckSchema = z.object({
    // Truck Ownership
    ownershipType: z.enum(["own", "subcontractor"]).default("own"),
    subcontractorId: z.string().optional(), // Required if ownershipType is 'subcontractor'

    //validate truck identification
    //validate truck identification
    licensePlate: z.string()
        .min(6, "License plate is required")
        .regex(
            /^([ก-ฮ]{2}|[0-9][ก-ฮ]{2})-?\d{1,4}$/,
            "License plate must be in format xx-xxxx or xxx-xxxx (e.g., กก-1234 or 1กก-1234)"
        ),
    province: z.string().min(1, "Province is required"),

    vin: z.string().length(17, "VIN must be exactly 17 characters").optional().or(z.literal("")), // Made optional for subcontractors
    engineNumber: z.string().length(10, "Engine number is required").optional().or(z.literal("")), // Made optional for subcontractors
    truckStatus: z.union([
        z.enum(["active", "inactive", "maintenance", "insurance-claim", "sold"]),
        z.literal(""),
    ]).refine((val) => val !== "", {
        message: "Truck status is required",
    }),
    //validate truck information
    brand: z.string().min(1, "Brand is required"),
    model: z.string().min(1, "Model is required"),
    year: z.string().regex(/^\d{4}$/, "Year must be a 4-digit number"),
    color: z.string().min(1, "Color is required"),
    type: z.string().min(1, "Type is required"),
    seats: z.string().refine(v => !v || (parseInt(v) >= 0 && parseInt(v) <= 10), "Seats must be 0-10 and cannot be negative"),
    //validate truck engines (required for own fleet, optional for subcontractors)
    fuelType: z.string().optional().default(""),
    engineCapacity: optionalNumber(0, 20000, "Engine capacity"),
    fuelCapacity: optionalNumber(0, 1000, "Fuel capacity"),
    maxLoadWeight: optionalNumber(0, 100000, "Max load weight"),
    //validate truck registration
    registrationDate: z.string().optional(), // Optional for Subcontractors
    buyingDate: z.string().optional(), // Optional for Subcontractors
    notes: z.string().optional(),
    // Images (Required for owned trucks, optional for subcontractor trucks)
    imageFrontRight: z.string().optional().default(""),
    imageFrontLeft: z.string().optional().default(""),
    imageBackRight: z.string().optional().default(""),
    imageBackLeft: z.string().optional().default(""),

    // Documents (Required for owned trucks, optional for subcontractor trucks)
    documentTax: z.string().optional().default(""),
    taxExpiryDate: z.string().optional(), // Tax (Act) Expiry Date
    documentRegister: z.string().optional().default(""),

    // Maintenance Book
    lastServiceDate: z.string().optional(),
    nextServiceDate: z.string().optional(),
    nextServiceMileage: optionalNumber(0, 2000000, "Next Service Mileage"),
    currentMileage: optionalNumber(0, 2000000, "Current Mileage"),

    // Insurance Information (Optional)
    insurancePolicyId: z.string().optional(), // maps to policy_id
    insurancePolicyNumber: z.string().optional(), // maps to policy_number
    insuranceCompany: z.string().optional(), // maps to provider
    insuranceType: z.enum(["1", "2", "2+", "3", "3+"]).or(z.literal("")).optional(), // maps to coverage_type
    insuranceStartDate: z.string().optional(), // maps to start_date
    insuranceExpiryDate: z.string().optional(), // maps to end_date
    insurancePremium: optionalNumber(0, 1000000, "Premium"), // maps to premium
    insuranceDocuments: z.array(z.string()).optional(), // maps to documents
    insuranceNotes: z.string().optional(), // maps to notes

    // Responsibility Assignment
    taxResponsible: z.string().optional().default("Operation Admin"),
    maintenanceResponsible: z.string().optional().default("Driver"),

    // Renewal Workflow Fields
    taxRenewalStatus: z.enum(["pending", "in_progress", "completed"]).optional(),
    insuranceRenewalStatus: z.enum(["pending", "in_progress", "completed"]).optional(),
    taxExpense: optionalNumber(0, 1000000, "Tax Expense"),
    taxReceipt: z.string().optional(), // URL to uploaded receipt
    paymentMethod: z.enum(["Cash", "Transfer", "Company Credit"]).or(z.literal("")).optional(),
    insuranceReceipt: z.string().optional(), // URL to uploaded receipt

    // Audit Trail
    statusHistory: z.array(z.object({
        status: z.string(),
        date: z.string(),
        changedBy: z.string(),
        notes: z.string().optional()
    })).optional(),
}).superRefine((data, ctx) => {
    // Conditional validation: images and documents required for own fleet only
    if (data.ownershipType === "own") {
        if (!data.imageFrontRight) {
            ctx.addIssue({ code: z.ZodIssueCode.custom, message: "Front-Right image is required for own fleet", path: ["imageFrontRight"] });
        }
        if (!data.imageFrontLeft) {
            ctx.addIssue({ code: z.ZodIssueCode.custom, message: "Front-Left image is required for own fleet", path: ["imageFrontLeft"] });
        }
        if (!data.imageBackRight) {
            ctx.addIssue({ code: z.ZodIssueCode.custom, message: "Back-Right image is required for own fleet", path: ["imageBackRight"] });
        }
        if (!data.imageBackLeft) {
            ctx.addIssue({ code: z.ZodIssueCode.custom, message: "Back-Left image is required for own fleet", path: ["imageBackLeft"] });
        }
        if (!data.documentTax) {
            ctx.addIssue({ code: z.ZodIssueCode.custom, message: "Tax document is required for own fleet", path: ["documentTax"] });
        }
        if (!data.taxExpiryDate) {
            ctx.addIssue({ code: z.ZodIssueCode.custom, message: "Tax Expiry Date is required for own fleet", path: ["taxExpiryDate"] });
        }
        if (!data.registrationDate) {
            ctx.addIssue({ code: z.ZodIssueCode.custom, message: "Registration Date is required for own fleet", path: ["registrationDate"] });
        }
        if (!data.documentRegister) {
            ctx.addIssue({ code: z.ZodIssueCode.custom, message: "Registration document is required for own fleet", path: ["documentRegister"] });
        }
        if (!data.fuelType) {
            ctx.addIssue({ code: z.ZodIssueCode.custom, message: "Fuel type is required for own fleet", path: ["fuelType"] });
        }
    }
    // Subcontractor trucks must have a subcontractor selected
    if (data.ownershipType === "subcontractor" && !data.subcontractorId) {
        ctx.addIssue({ code: z.ZodIssueCode.custom, message: "Subcontractor is required", path: ["subcontractorId"] });
    }
});

// Type inference from schema - use input type for form values
export type TruckFormValues = z.input<typeof truckSchema>;

// Type for validated data (after form submission passes validation)
export type TruckValidatedData = z.infer<typeof truckSchema>;

// Default values for the form
export const truckDefaultValues: TruckFormValues = {
    ownershipType: "own",
    subcontractorId: "",
    licensePlate: "",
    province: "",

    vin: "",
    engineNumber: "",
    truckStatus: "",
    brand: "",
    model: "",
    year: "",
    color: "",
    type: "",
    seats: "",
    fuelType: "",
    engineCapacity: undefined,
    fuelCapacity: undefined,
    maxLoadWeight: undefined,
    registrationDate: "",
    buyingDate: "",
    notes: "",
    // Insurance
    insurancePolicyId: "",
    insurancePolicyNumber: "",
    insuranceCompany: "",
    insuranceType: "",
    insuranceStartDate: "",
    insuranceExpiryDate: "",
    insurancePremium: undefined,
    insuranceDocuments: [],
    insuranceNotes: "",

    // Images
    imageFrontRight: "",
    imageFrontLeft: "",
    imageBackRight: "",
    imageBackLeft: "",
    // Documents
    documentTax: "",
    taxExpiryDate: "",
    documentRegister: "",
    lastServiceDate: "",
    nextServiceDate: "",
    nextServiceMileage: undefined,
    currentMileage: 0,

    // Renewal Defaults
    taxRenewalStatus: undefined,
    insuranceRenewalStatus: undefined,
    taxExpense: undefined,
    taxReceipt: "",
    paymentMethod: "",
    insuranceReceipt: "",
    statusHistory: [],
};
