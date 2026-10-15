import { describe, expect, it } from "vitest";
import { truckDefaultValues, truckSchema } from "./truckSchema";

const record = {
    ...truckDefaultValues, licensePlate: "1กก-1234", province: "กรุงเทพมหานคร",
    brand: "Isuzu", model: "FRR", year: "2023", color: "ขาว", type: "6 Wheels",
    truckStatus: "active", engineNumber: "12345678901234567",
};

describe("Go truck form contract", () => {
    it("accepts an existing Go-valid record without photos, documents or PM fields", () => {
        expect(truckSchema.safeParse(record).success).toBe(true);
    });
    it("accepts the complete eight-character plate example", () => {
        expect(record.licensePlate).toHaveLength(8);
        expect(truckSchema.safeParse(record).success).toBe(true);
    });
    it.each(["70-1234", "1กก-12345", "1กก-1234x"])("rejects invalid plate %s", (licensePlate) => {
        expect(truckSchema.safeParse({ ...record, licensePlate }).success).toBe(false);
    });
    it("rejects engine numbers over 50 characters", () => {
        expect(truckSchema.safeParse({ ...record, engineNumber: "a".repeat(51) }).success).toBe(false);
    });
    it.each(["O", "I", "Q"])("accepts Go-valid VINs containing %s and normalizes case", (letter) => {
        const vin = `${letter.toLowerCase()}1234567890123456`;
        const parsed = truckSchema.parse({ ...record, vin });
        expect(parsed.vin).toBe(vin.toUpperCase());
    });
});
