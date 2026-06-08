// Package pbconvert holds proto↔domain conversion helpers shared by both
// the httpapi and grpcapi transport packages.
package pbconvert

import (
	pb "github.com/BrandonDHaskell/Portunus/server/api/portunus/v1"
	"github.com/BrandonDHaskell/Portunus/server/internal/portunus/types"
)

// DomainProvisionStatusToProto maps a domain ProvisionStatus to its proto
// representation. Both transports call this to avoid duplicating the switch.
func DomainProvisionStatusToProto(s types.ProvisionStatus) pb.ProvisionStatus {
	switch s {
	case types.ProvisionStatusSuccess:
		return pb.ProvisionStatus_PROVISION_STATUS_SUCCESS
	case types.ProvisionStatusPendingCreated:
		return pb.ProvisionStatus_PROVISION_STATUS_PENDING_CREATED
	case types.ProvisionStatusDuplicateActive:
		return pb.ProvisionStatus_PROVISION_STATUS_DUPLICATE_ACTIVE
	case types.ProvisionStatusDuplicateInactive:
		return pb.ProvisionStatus_PROVISION_STATUS_DUPLICATE_INACTIVE
	case types.ProvisionStatusDuplicatePending:
		return pb.ProvisionStatus_PROVISION_STATUS_DUPLICATE_PENDING
	case types.ProvisionStatusUnauthorized:
		return pb.ProvisionStatus_PROVISION_STATUS_UNAUTHORIZED
	case types.ProvisionStatusInvalidRole:
		return pb.ProvisionStatus_PROVISION_STATUS_INVALID_ROLE
	default:
		return pb.ProvisionStatus_PROVISION_STATUS_UNSPECIFIED
	}
}

// ProtoProvisionModeToDomaon maps a proto ProvisionMode to its domain type.
func ProtoProvisionModeToDomain(m pb.ProvisionMode) types.ProvisionMode {
	switch m {
	case pb.ProvisionMode_PROVISION_MODE_CAPTURE:
		return types.ProvisionModeCapture
	case pb.ProvisionMode_PROVISION_MODE_OPERATOR_ENROLL:
		return types.ProvisionModeOperatorEnroll
	default:
		return types.ProvisionModeUnspecified
	}
}
