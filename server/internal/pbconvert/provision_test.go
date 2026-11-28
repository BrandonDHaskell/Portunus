package pbconvert_test

import (
	"testing"

	pb "github.com/BrandonDHaskell/Portunus/server/api/portunus/v1"
	"github.com/BrandonDHaskell/Portunus/server/internal/pbconvert"
	"github.com/BrandonDHaskell/Portunus/server/internal/portunus/types"
)

// Every domain status must map to its own wire value; an unmapped status
// would reach the PEU as UNSPECIFIED and be shown as an unknown outcome.
func TestDomainProvisionStatusToProto(t *testing.T) {
	cases := map[types.ProvisionStatus]pb.ProvisionStatus{
		types.ProvisionStatusPendingCreated:    pb.ProvisionStatus_PROVISION_STATUS_PENDING_CREATED,
		types.ProvisionStatusDuplicateActive:   pb.ProvisionStatus_PROVISION_STATUS_DUPLICATE_ACTIVE,
		types.ProvisionStatusDuplicateInactive: pb.ProvisionStatus_PROVISION_STATUS_DUPLICATE_INACTIVE,
		types.ProvisionStatusDuplicatePending:  pb.ProvisionStatus_PROVISION_STATUS_DUPLICATE_PENDING,
		types.ProvisionStatusUnauthorized:      pb.ProvisionStatus_PROVISION_STATUS_UNAUTHORIZED,
		"":                                     pb.ProvisionStatus_PROVISION_STATUS_UNSPECIFIED,
		"something_new":                        pb.ProvisionStatus_PROVISION_STATUS_UNSPECIFIED,
	}
	seen := map[pb.ProvisionStatus]types.ProvisionStatus{}
	for in, want := range cases {
		got := pbconvert.DomainProvisionStatusToProto(in)
		if got != want {
			t.Errorf("%q -> %v, want %v", in, got, want)
		}
		if want != pb.ProvisionStatus_PROVISION_STATUS_UNSPECIFIED {
			if prev, dup := seen[got]; dup {
				t.Errorf("%q and %q both map to %v", prev, in, got)
			}
			seen[got] = in
		}
	}
}
