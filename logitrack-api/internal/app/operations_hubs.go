package app

import (
	"context"

	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/authz"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/hubs"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/operations"
)

// OperationsHubs hands the hub master of the hubs service to the operations reads (T30: the place keys and labels of
// the boards, the trips and the monitor). internal/hubs depends on internal/operations (through internal/iam), so the
// operations package names its own Hub and this adapter joins them.
type OperationsHubs struct{ Service *hubs.Service }

// Hubs is every hub and SOC of the master as p sees it (hubs.Service.List without a page: the shared cache, the
// per-principal mask).
func (a OperationsHubs) Hubs(ctx context.Context, p *authz.Principal) ([]operations.Hub, error) {
	list, _, err := a.Service.List(ctx, p, hubs.ListInput{})
	if err != nil {
		return nil, err
	}
	out := make([]operations.Hub, len(list))
	for i, h := range list {
		out[i] = operations.Hub{ID: h.ID, Code: h.SourceID, NameTh: h.SourceNameTh, NameEn: h.SourceNameEn,
			LinkedCustomerName: h.LinkedCustomerName, LinkedPartyID: h.LinkedPartyID, StationType: h.StationType}
	}
	return out, nil
}
