import type { TruckImportResult } from "../api/trucks";
import { fieldReasonText } from "@/lib/formServerErrors";

type Translate = (key: string, params?: string | Record<string, string | number>) => string;
const aliases: Record<string, string[]> = {
    licensePlate: ["licenseplate", "plate", "plateno", "platenumber", "ทะเบียน", "ทะเบียนรถ", "เลขทะเบียน"],
    province: ["province", "จังหวัด"], type: ["type", "trucktype", "vehicletype", "ประเภท", "ประเภทรถ"],
    brand: ["brand", "ยี่ห้อ"], model: ["model", "รุ่น"], color: ["color", "colour", "สี"],
    year: ["year", "ปี", "ปีรถ", "ปีผลิต"], vin: ["vin", "chassisnumber", "เลขตัวถัง"],
};
export function importPreviewRow(row: Record<string, unknown>): Record<string, string> {
    const out: Record<string, string> = {};
    for (const key of Object.keys(row).sort()) {
        const normalized = key.toLowerCase().replace(/[^\p{L}\p{N}\p{M}]/gu, "");
        const field = Object.keys(aliases).find((field) => aliases[field].includes(normalized));
        if (field && !(field in out)) out[field] = String(row[key] ?? "");
    }
    return out;
}

export function importFieldErrorText(field: TruckImportResult["errors"][number]["fields"][number], t: Translate): string {
    const fieldKeys: Record<string, string> = {
        licensePlate: "trucks.section.licensePlate", province: "trucks.section.province", type: "trucks.section.truckType",
        brand: "trucks.section.brand", model: "trucks.section.model", color: "trucks.section.color", year: "trucks.section.year",
        vin: "trucks.section.vin", engineNumber: "trucks.section.engineNumber", fuelType: "trucks.section.fuelType",
        ownership: "trucks.edit.ownership", status: "trucks.section.truckStatus", registrationDate: "trucks.section.regDate",
        buyingDate: "trucks.section.buyingDate", maxLoadWeightKg: "trucks.section.maxLoad", engineCapacity: "trucks.section.engineCap",
        fuelCapacity: "trucks.section.fuelCap", seats: "trucks.section.seats", gpsVehicleId: "trucks.section.gpsVehicleId",
        taxExpiryDate: "trucks.section.taxExpiry", insuranceExpiryDate: "trucks.section.endDate", currentMileage: "trucks.maintenance.currentMileage", notes: "trucks.section.notes",
    };
    const label = t(fieldKeys[field.field] ?? "trucks.import.otherField");
    const reason = field.reason === "duplicate_in_file" ? t("trucks.import.duplicateInFile", { row: Number(field.params?.row ?? 0) }) : fieldReasonText(field.reason, t, field.params);
    return `${label}: ${reason}`;
}
