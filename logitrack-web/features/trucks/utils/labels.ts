type Translate = (key: string) => string;
// Display aliases only: keep list/renewal labels independent of task validation.
const typeKeys: Record<string, string> = {
    pickup: "pickup", "4w": "pickup", "4-wheel": "4wheels", "4 wheels": "4wheels",
    "4 wheels jumbo": "4wheels", "4wj": "4wheels", "4-wheel-jumbo": "4wheels",
    "6 wheels": "6wheels", "6w": "6wheels", "6wh": "6wheels", "6-wheel": "6wheels",
    "10 wheels": "10wheels", "10w": "10wheels", "10wh": "10wheels", "10-wheel": "10wheels",
    "18 wheels": "18wheels", "18wh": "18wheels", van: "van",
};
export function truckTypeLabel(value: string | null | undefined, t: Translate): string {
    if (!value) return t("trucks.ui4.notAvailable");
    const key = typeKeys[value.trim().toLowerCase()];
    return key ? t(`trucks.type.${key}`) : value;
}
export function truckFuelLabel(value: string | null | undefined, t: Translate): string {
    if (!value) return t("trucks.ui4.notAvailable");
    const key = value.toLowerCase();
    return ["diesel", "gasoline", "ngv", "electric", "hybrid"].includes(key) ? t(`trucks.fuel.${key}`) : value;
}
