export { default as TrucksListDashboard } from "./components/TrucksListDashboard";
export * from "./hooks/useTrucksList";
export * from "./services/truckService";
export * from "./components/TruckComplianceCards";
// TruckImportDialog is not re-exported: it carries xlsx and TrucksListDashboard loads it with
// next/dynamic (developer-spec.md §10.11, TW9).
