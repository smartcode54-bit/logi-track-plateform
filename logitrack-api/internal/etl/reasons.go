package etl

// Reason is a quarantine reason code: the canonical merged list of Appendix A §A.3.0 (R68), which the CHECK on
// etl.quarantine.reason_code (0009_infra) enforces too. reasons_test.go reads the list from the appendix and from
// the migration and fails on any drift; draft synonyms (tenant_orphan, invalid_timestamp, enum_unknown, ...) are
// never written.
type Reason string

// The reason codes, in the order of Appendix A §A.3.0.
const (
	ReasonTenantUnresolved      Reason = "tenant_unresolved"
	ReasonDriverUnresolved      Reason = "driver_unresolved"
	ReasonUserUnresolved        Reason = "user_unresolved"
	ReasonTruckUnresolved       Reason = "truck_unresolved"
	ReasonTaskUnresolved        Reason = "task_unresolved"
	ReasonTaskAmbiguous         Reason = "task_ambiguous"
	ReasonTripUnresolved        Reason = "trip_unresolved"
	ReasonPartyUnresolved       Reason = "party_unresolved"
	ReasonCustomerUnresolved    Reason = "customer_unresolved"
	ReasonHubUnresolved         Reason = "hub_unresolved"
	ReasonTenantMismatch        Reason = "tenant_mismatch"
	ReasonHelperOverflow        Reason = "helper_overflow"
	ReasonHelperTenantMismatch  Reason = "helper_tenant_mismatch"
	ReasonMissingRequired       Reason = "missing_required"
	ReasonBadTimestamp          Reason = "bad_timestamp"
	ReasonBadNumber             Reason = "bad_number"
	ReasonNegativeMoney         Reason = "negative_money"
	ReasonStatusOutOfVocab      Reason = "status_out_of_vocab"
	ReasonVehicleClassOutOfEnum Reason = "vehicle_class_out_of_enum"
	ReasonPhotoTypeUnknown      Reason = "photo_type_unknown"
	ReasonFileMissingAtSource   Reason = "file_missing_at_source"
	ReasonURLUnparseable        Reason = "url_unparseable"
	ReasonDuplicateNaturalKey   Reason = "duplicate_natural_key"
	ReasonDuplicateServiceFee   Reason = "duplicate_service_fee"
	ReasonDuplicateHubSourceID  Reason = "duplicate_hub_source_id"
	ReasonCreatedAtDerived      Reason = "created_at_derived"
	ReasonMissingDeliveredAt    Reason = "missing_delivered_at"
	ReasonMissingBillingDate    Reason = "missing_billing_date"
	ReasonMissingEndedAt        Reason = "missing_ended_at"
	ReasonBillingDateLocked     Reason = "billing_date_locked"
	ReasonTripNoMismatch        Reason = "trip_no_mismatch"
	ReasonMultidropAsSingle     Reason = "multidrop_priced_as_single"
	ReasonLinkBlanketDefault    Reason = "link_blanket_default"
	ReasonLegacyStandbyTrip     Reason = "legacy_standby_trip"
	ReasonUnknownCapabilityKey  Reason = "unknown_capability_key"
	ReasonUnknownCollection     Reason = "unknown_collection"
)

// Reasons is the whole catalog in Appendix A order.
var Reasons = []Reason{
	ReasonTenantUnresolved, ReasonDriverUnresolved, ReasonUserUnresolved, ReasonTruckUnresolved, ReasonTaskUnresolved,
	ReasonTaskAmbiguous, ReasonTripUnresolved, ReasonPartyUnresolved, ReasonCustomerUnresolved, ReasonHubUnresolved,
	ReasonTenantMismatch, ReasonHelperOverflow, ReasonHelperTenantMismatch, ReasonMissingRequired,
	ReasonBadTimestamp, ReasonBadNumber, ReasonNegativeMoney, ReasonStatusOutOfVocab, ReasonVehicleClassOutOfEnum,
	ReasonPhotoTypeUnknown, ReasonFileMissingAtSource, ReasonURLUnparseable,
	ReasonDuplicateNaturalKey, ReasonDuplicateServiceFee, ReasonDuplicateHubSourceID,
	ReasonCreatedAtDerived, ReasonMissingDeliveredAt, ReasonMissingBillingDate, ReasonMissingEndedAt,
	ReasonBillingDateLocked, ReasonTripNoMismatch, ReasonMultidropAsSingle, ReasonLinkBlanketDefault,
	ReasonLegacyStandbyTrip, ReasonUnknownCapabilityKey, ReasonUnknownCollection,
}

// Outcome is a document's load outcome (etl.source_docs.status, Appendix A §A.3.0). Pending marks a document of a
// collection whose mapping lands in a later task: recorded, not yet loaded, and processed by the first load that
// knows the collection.
type Outcome string

// The outcomes.
const (
	Loaded      Outcome = "loaded"
	Quarantined Outcome = "quarantined"
	Rejected    Outcome = "rejected"
	Dropped     Outcome = "dropped"
	Pending     Outcome = "pending"
)

// Finding is one etl.quarantine row: Field "" is a whole-row finding.
type Finding struct {
	Field  string
	Reason Reason
	Detail string
	Raw    any // the offending legacy value, kept as tagged JSON in raw_value
}
