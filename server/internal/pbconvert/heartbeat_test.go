package pbconvert

import (
	"testing"

	pb "github.com/BrandonDHaskell/Portunus/server/api/portunus/v1"
	"google.golang.org/protobuf/proto"
)

// Every heartbeat field reaches the domain type, and an omitted optional
// field stays nil rather than reading as false or zero.
func TestHeartbeatRequestFromProto(t *testing.T) {
	t.Run("all fields", func(t *testing.T) {
		got := HeartbeatRequestFromProto(&pb.HeartbeatRequest{
			ModuleId:        "door-001",
			FirmwareVersion: "1.2.3",
			UptimeS:         42,
			DoorClosed:      proto.Bool(true),
			RssiDbm:         proto.Int32(-61),
			Ip:              "10.0.0.9",
			FreeHeapBytes:   12345,
			Sequence:        7,
			ReaderOk:        proto.Bool(false),
			StrikeOk:        proto.Bool(false),
			DroppedEvents:   3,
		})
		if got.ModuleID != "door-001" || got.FirmwareVersion != "1.2.3" || got.UptimeSeconds != 42 ||
			got.IP != "10.0.0.9" || got.FreeHeapBytes != 12345 || got.Sequence != 7 || got.DroppedEvents != 3 {
			t.Errorf("scalar fields not mapped: %+v", got)
		}
		if got.DoorClosed == nil || !*got.DoorClosed {
			t.Errorf("DoorClosed = %v, want true", got.DoorClosed)
		}
		if got.RSSIDbm == nil || *got.RSSIDbm != -61 {
			t.Errorf("RSSIDbm = %v, want -61", got.RSSIDbm)
		}
		if got.ReaderOK == nil || *got.ReaderOK {
			t.Errorf("ReaderOK = %v, want false", got.ReaderOK)
		}
		if got.StrikeOK == nil || *got.StrikeOK {
			t.Errorf("StrikeOK = %v, want false", got.StrikeOK)
		}
	})

	t.Run("optional fields omitted", func(t *testing.T) {
		got := HeartbeatRequestFromProto(&pb.HeartbeatRequest{ModuleId: "door-001"})
		if got.DoorClosed != nil || got.RSSIDbm != nil || got.ReaderOK != nil || got.StrikeOK != nil {
			t.Errorf("omitted optionals should be nil: %+v", got)
		}
	})
}
