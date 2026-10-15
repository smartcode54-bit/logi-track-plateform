import * as z from "zod";
import { TRUCK_PLATE_MAX_LENGTH, TRUCK_PLATE_REGEX } from "@/features/trucks/utils/plate";

type Translate = (key: string) => string;

/** The form sends a full body on create and edit; nullable Go fields stay optional in both. */
export function createTruckSchema(t: Translate = (key) => key) {
    const reasonKeys: Record<string, string> = {
        required: "required", tooLong: "too_long", invalid: "invalid", number: "invalid", range: "out_of_range",
        integer: "invalid", precision: "too_precise", date: "invalid", year: "out_of_range", seats: "out_of_range", partner: "required",
    };
    const message = (key: string) => t(reasonKeys[key] ? `masterData.fieldReason.${reasonKeys[key]}` : `trucks.validation.${key}`);
    const required = (max = 100) => z.string({ error: message("required") }).trim()
        .min(1, message("required")).max(max, message("tooLong"));
    const optionalText = (max: number) => z.string().trim().max(max, message("tooLong")).optional();
    const number = (min: number, max: number, integer = false) =>
        z.union([z.string(), z.number(), z.null(), z.undefined()])
            .transform((value) => value == null || value === "" ? undefined : Number(value))
            .pipe(z.number({ error: message("number") }).min(min, message("range"))
                .max(max, message("range"))
                .refine((value) => integer ? Number.isInteger(value) : Math.abs(value * 100 - Math.round(value * 100)) < 0.000001,
                    message(integer ? "integer" : "precision")).optional());
    const date = z.string().refine((value) => !value || /^\d{4}-\d{2}-\d{2}$/.test(value) &&
        !Number.isNaN(Date.parse(value)) && new Date(value).toISOString().slice(0, 10) === value,
        message("date")).optional();

    return z.object({
        ownershipType: z.enum(["own", "subcontractor"], { error: message("invalid") }).default("own"),
        subcontractorId: z.string().optional(),
        licensePlate: required(TRUCK_PLATE_MAX_LENGTH).min(6, message("plate"))
            .regex(TRUCK_PLATE_REGEX, message("plate")),
        province: required(),
        vin: z.string().trim().toUpperCase().refine((value) => !value || /^[A-Z0-9]{17}$/.test(value), message("vin")).optional(),
        engineNumber: optionalText(50),
        GPSVehicleId: optionalText(64),
        truckStatus: z.enum(["active", "inactive", "maintenance", "insurance-claim", "sold", ""], { error: message("invalid") })
            .refine((value) => value !== "", message("required")),
        brand: required(),
        model: required(),
        year: z.string({ error: message("year") }).regex(/^\d{4}$/, message("year"))
            .refine((value) => Number(value) >= 1900 && Number(value) <= 2200, message("year")),
        color: required(),
        type: required(),
        seats: z.string().refine((value) => !value || /^\d+$/.test(value) && Number(value) <= 10, message("seats")),
        fuelType: optionalText(50).default(""),
        engineCapacity: number(0, 20000),
        fuelCapacity: number(0, 1000),
        maxLoadWeight: number(0, 100000),
        registrationDate: date,
        buyingDate: date,
        notes: optionalText(2000),
        imageFrontRight: z.string().optional().default(""),
        imageFrontLeft: z.string().optional().default(""),
        imageBackRight: z.string().optional().default(""),
        imageBackLeft: z.string().optional().default(""),
        documentTax: z.string().optional().default(""),
        taxExpiryDate: date,
        documentRegister: z.string().optional().default(""),
        lastServiceDate: date,
        nextServiceDate: date,
        nextServiceMileage: number(0, 2000000, true),
        pmIntervalKm: number(1, 500000, true),
        currentMileage: number(0, 2000000, true),
        insurancePolicyId: optionalText(100),
        insurancePolicyNumber: optionalText(100),
        insuranceCompany: optionalText(200),
        insuranceType: z.enum(["1", "2", "2+", "3", "3+", ""], { error: message("invalid") }).optional(),
        insuranceStartDate: date,
        insuranceExpiryDate: date,
        insurancePremium: number(0, 1000000),
        insuranceDocuments: z.array(z.string()).optional(),
        insuranceNotes: optionalText(2000),
        taxResponsible: optionalText(100).default(""),
        maintenanceResponsible: optionalText(100).default(""),
        taxRenewalStatus: z.enum(["pending", "in_progress", "completed"], { error: message("invalid") }).optional(),
        insuranceRenewalStatus: z.enum(["pending", "in_progress", "completed"], { error: message("invalid") }).optional(),
        taxExpense: number(0, 1000000),
        taxReceipt: z.string().optional(),
        paymentMethod: z.enum(["Cash", "Transfer", "Company Credit", ""], { error: message("invalid") }).optional(),
        insuranceReceipt: z.string().optional(),
        statusHistory: z.array(z.object({ status: z.string(), date: z.string(), changedBy: z.string(), notes: z.string().optional() })).optional(),
    }).superRefine((data, ctx) => {
        if (data.ownershipType === "subcontractor" && !data.subcontractorId) {
            ctx.addIssue({ code: "custom", message: message("partner"), path: ["subcontractorId"] });
        }
    });
}

export const truckSchema = createTruckSchema();
export type TruckFormValues = z.input<typeof truckSchema>;
export type TruckValidatedData = z.infer<typeof truckSchema>;

// Default values for the form
export const truckDefaultValues: TruckFormValues = {
    ownershipType: "own",
    subcontractorId: "",
    licensePlate: "",
    province: "",

    vin: "",
    engineNumber: "",
    GPSVehicleId: "",
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
    pmIntervalKm: undefined,
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
