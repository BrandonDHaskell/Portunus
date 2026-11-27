package grpcapi_test

import (
	"bytes"
	"context"
	"database/sql"
	"fmt"
	"io"
	"log"
	"net"
	"strings"
	"testing"

	_ "modernc.org/sqlite"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/peer"
	"google.golang.org/grpc/status"

	pb "github.com/BrandonDHaskell/Portunus/server/api/portunus/v1"
	"github.com/BrandonDHaskell/Portunus/server/internal/db"
	"github.com/BrandonDHaskell/Portunus/server/internal/grpcapi"
	"github.com/BrandonDHaskell/Portunus/server/internal/portunus/service"
	"github.com/BrandonDHaskell/Portunus/server/internal/portunus/store/memory"
	sqlitestore "github.com/BrandonDHaskell/Portunus/server/internal/portunus/store/sqlite"
)

var testCredSecret = []byte("grpcapi-test-credential-secret")

// newSQLiteServer wires every gRPC dependency against in-memory SQLite with
// the real migrations, the way main.go does, and returns the server and DB.
func newSQLiteServer(t *testing.T) (*grpcapi.Server, *sql.DB) {
	t.Helper()
	safe := strings.NewReplacer("/", "_", " ", "_").Replace(t.Name())
	conn, err := sql.Open("sqlite", fmt.Sprintf("file:grpc_%s?mode=memory&cache=shared&_pragma=foreign_keys(1)", safe))
	if err != nil {
		t.Fatal(err)
	}
	conn.SetMaxOpenConns(1)
	if err := db.Migrate(context.Background(), conn); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	w := db.NewWorker(conn)
	t.Cleanup(func() { w.Close(); conn.Close() })

	registry := service.NewDeviceRegistry(sqlitestore.NewDeviceStore(conn, w))
	members := sqlitestore.NewMemberAccessStore(conn, w)
	events := sqlitestore.NewAccessEventStore(conn, w)
	access := service.NewAccessService(registry, service.AccessPolicy{}, events)
	access.SetCredentialHashSecret(testCredSecret)
	access.SetMemberAccessStore(members)
	access.SetModuleAuthStore(sqlitestore.NewModuleAuthorizationStore(conn, w))

	srv := grpcapi.NewServer(grpcapi.Dependencies{
		Logger:           log.New(io.Discard, "", 0),
		HeartbeatService: service.NewHeartbeatService(sqlitestore.NewHeartbeatStore(conn, w), registry),
		AccessService:    access,
		ProvisionService: service.NewProvisionService(registry, members, events, testCredSecret, sqlitestore.NewAuditStore(conn, w)),
	})
	return srv, conn
}

func seedModuleRow(t *testing.T, conn *sql.DB, id, moduleType string) {
	t.Helper()
	if _, err := conn.Exec(`INSERT INTO modules(module_id, enabled, commissioned_at_ms, module_type, created_at_ms, updated_at_ms) VALUES (?,1,1,?,0,0)`, id, moduleType); err != nil {
		t.Fatalf("seed module: %v", err)
	}
}

func TestSendHeartbeat(t *testing.T) {
	srv, conn := newSQLiteServer(t)
	seedModuleRow(t, conn, "door-001", "access_control_unit")

	resp, err := srv.SendHeartbeat(context.Background(), &pb.HeartbeatRequest{ModuleId: "door-001", Sequence: 1})
	if err != nil {
		t.Fatalf("SendHeartbeat: %v", err)
	}
	if !resp.GetOk() || !resp.GetKnown() || resp.GetModuleId() != "door-001" || resp.GetServerTime() == "" {
		t.Fatalf("unexpected %+v", resp)
	}

	_, err = srv.SendHeartbeat(context.Background(), &pb.HeartbeatRequest{ModuleId: "  "})
	assertCode(t, err, codes.InvalidArgument)
}

func TestProvisionCredential(t *testing.T) {
	srv, conn := newSQLiteServer(t)
	seedModuleRow(t, conn, "peu-001", "provisioning_enrollment_unit")
	seedModuleRow(t, conn, "acu-001", "access_control_unit")
	uid := []byte{0x04, 0xA3, 0x2B, 0x1C}
	ctx := context.Background()

	resp, err := srv.ProvisionCredential(ctx, &pb.ProvisionCredentialRequest{ModuleId: "peu-001", CredentialUid: uid})
	if err != nil {
		t.Fatalf("ProvisionCredential: %v", err)
	}
	if resp.GetMemberUuid() == "" || resp.GetStatus() != pb.ProvisionStatus_PROVISION_STATUS_PENDING_CREATED {
		t.Fatalf("unexpected %+v", resp)
	}

	resp, err = srv.ProvisionCredential(ctx, &pb.ProvisionCredentialRequest{ModuleId: "peu-001", CredentialUid: uid})
	if err != nil {
		t.Fatalf("duplicate scan: %v", err)
	}
	if resp.GetStatus() != pb.ProvisionStatus_PROVISION_STATUS_DUPLICATE_PENDING {
		t.Fatalf("duplicate scan status = %v, want DUPLICATE_PENDING", resp.GetStatus())
	}

	resp, err = srv.ProvisionCredential(ctx, &pb.ProvisionCredentialRequest{ModuleId: "acu-001", CredentialUid: uid})
	if err != nil {
		t.Fatalf("ACU provision: %v", err)
	}
	if resp.GetStatus() != pb.ProvisionStatus_PROVISION_STATUS_UNAUTHORIZED || resp.GetMemberUuid() != "" {
		t.Fatalf("ACU must not provision: %+v", resp)
	}

	_, err = srv.ProvisionCredential(ctx, &pb.ProvisionCredentialRequest{ModuleId: "peu-001"})
	assertCode(t, err, codes.InvalidArgument)
	_, err = srv.ProvisionCredential(ctx, &pb.ProvisionCredentialRequest{CredentialUid: uid})
	assertCode(t, err, codes.InvalidArgument)
	_, err = srv.ProvisionCredential(ctx, &pb.ProvisionCredentialRequest{ModuleId: "ghost", CredentialUid: uid})
	assertCode(t, err, codes.NotFound)
}

func TestRequestAccess_ErrorMapping(t *testing.T) {
	srv, conn := newSQLiteServer(t)
	seedModuleRow(t, conn, "door-001", "access_control_unit")
	ctx := context.Background()

	_, err := srv.RequestAccess(ctx, &pb.AccessRequest{CredentialId: "04:A3:2B:1C"})
	assertCode(t, err, codes.InvalidArgument)
	_, err = srv.RequestAccess(ctx, &pb.AccessRequest{ModuleId: "door-001"})
	assertCode(t, err, codes.InvalidArgument)

	resp, err := srv.RequestAccess(ctx, &pb.AccessRequest{ModuleId: "door-001", CredentialId: "04:A3:2B:1C"})
	if err != nil {
		t.Fatalf("RequestAccess: %v", err)
	}
	if resp.GetGranted() || resp.GetReason() != "credential_not_found" {
		t.Fatalf("unenrolled card: granted=%v reason=%q", resp.GetGranted(), resp.GetReason())
	}
}

// A service that skipped Validate() must surface as Internal, never a grant.
func TestRequestAccess_MisconfiguredServiceIsInternal(t *testing.T) {
	registry := service.NewDeviceRegistry(memory.NewDeviceStore([]string{"door-001"}))
	srv := grpcapi.NewServer(grpcapi.Dependencies{
		Logger:        log.New(io.Discard, "", 0),
		AccessService: service.NewAccessService(registry, service.AccessPolicy{}, memory.NewAccessEventStore()),
	})
	resp, err := srv.RequestAccess(context.Background(), &pb.AccessRequest{ModuleId: "door-001", CredentialId: "04:A3:2B:1C"})
	assertCode(t, err, codes.Internal)
	if resp != nil {
		t.Fatalf("got a response alongside the error: %+v", resp)
	}
}

func TestLoggingInterceptor(t *testing.T) {
	var buf bytes.Buffer
	ic := grpcapi.LoggingInterceptor(log.New(&buf, "", 0))
	ctx := peer.NewContext(context.Background(), &peer.Peer{Addr: &net.TCPAddr{IP: net.IPv4(10, 0, 0, 9), Port: 5555}})
	info := &grpc.UnaryServerInfo{FullMethod: "/portunus.v1.PortunusService/RequestAccess"}

	_, err := ic(ctx, nil, info, func(context.Context, any) (any, error) {
		return nil, status.Error(codes.PermissionDenied, "nope")
	})
	if status.Code(err) != codes.PermissionDenied {
		t.Fatalf("interceptor altered the handler error: %v", err)
	}
	line := buf.String()
	for _, want := range []string{info.FullMethod, "from=10.0.0.9:5555", "status=PermissionDenied"} {
		if !strings.Contains(line, want) {
			t.Errorf("log line %q missing %q", line, want)
		}
	}

	buf.Reset()
	_, _ = ic(context.Background(), nil, info, func(context.Context, any) (any, error) { return "ok", nil })
	if !strings.Contains(buf.String(), "from=unknown") || !strings.Contains(buf.String(), "status=OK") {
		t.Errorf("no-peer log line = %q", buf.String())
	}
}
