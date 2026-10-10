// Map clients (leaflet) are loaded with next/dynamic by their wrappers; re-exporting them here put
// leaflet into the initial JS of every page importing this barrel (developer-spec.md §10.11, TW9).
export { IncidentLocationMap } from "./components/IncidentLocationMap";
