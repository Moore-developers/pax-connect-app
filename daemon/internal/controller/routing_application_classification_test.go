package controller

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"google.golang.org/protobuf/types/known/emptypb"
	pb "pax-connect/daemon/gen/proto/paxv1"
	"pax-connect/daemon/internal/config"
)

func TestRoutingApplicationsOnlyUserInstalledBundles(t *testing.T) {
	c, _ := routingController(t)

	ctx := context.Background()
	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err = os.Chmod(dir, 0700); err != nil {
		t.Fatal(err)
	}
	if err = c.ConfigureHistory(ctx, dir, uint32(os.Geteuid())); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = c.CloseHistory(ctx) })
	root := filepath.Join(dir, "Applications")
	if err = os.MkdirAll(root, 0700); err != nil {
		t.Fatal(err)
	}
	bundle := filepath.Join(root, "User.app")
	if err = os.MkdirAll(filepath.Join(bundle, "Contents"), 0700); err != nil {
		t.Fatal(err)
	}
	plist := []byte(`<?xml version="1.0"?><plist version="1.0"><dict><key>CFBundleIdentifier</key><string>fixture.user</string><key>CFBundleName</key><string>User</string><key>CFBundlePackageType</key><string>APPL</string></dict></plist>`)
	if err = os.WriteFile(filepath.Join(bundle, "Contents", "Info.plist"), plist, 0600); err != nil {
		t.Fatal(err)
	}
	// A system app alias in /Applications must not pass the installation boundary.
	systemID := ""
	for _, system := range []string{"/System/Applications/Utilities/Terminal.app", "/System/Applications/Calculator.app"} {
		if _, statErr := os.Stat(system); statErr != nil {
			continue
		}
		if err = os.Symlink(system, filepath.Join(root, "System Alias.app")); err != nil {
			t.Fatal(err)
		}
		f := config.FlowFact{Process: filepath.Join(system, "Contents/MacOS/pax-identity")}
		identifyHistoryApplication(&f)
		systemID = f.ApplicationID
		break
	}
	c.routing.applicationQuery = func(ctx context.Context) []config.RoutingApplication {
		return c.installedRoutingApplications(ctx, []string{root})
	}
	tool := filepath.Join(dir, "tool")
	dnsTool := filepath.Join(dir, "dns-control")
	for _, p := range []string{tool, dnsTool} {
		if err = os.WriteFile(p, []byte("fixture"), 0700); err != nil {
			t.Fatal(err)
		}
	}
	flows := []config.FlowFact{
		{Process: tool, ApplicationID: "process:" + tool, Application: "tool"},
		{Process: dnsTool, ApplicationID: "process:" + dnsTool, Application: "dns-control", Purpose: "dns"},
	}
	if _, err := os.Stat("/usr/libexec/trustd"); err == nil {
		flows = append(flows, config.FlowFact{Process: "/usr/libexec/trustd", ApplicationID: "com.apple.trustd", Application: "trustd"})
	}
	for i, f := range flows {
		f.Key = f.Process
		f.Domain = "example.test"
		f.StartedAt = time.Now().Add(-time.Minute)
		if err := FeedHistoryFixture(t, c, ctx, config.ObservationBatch{Session: "fixture", HighWater: uint64(i + 1), Collecting: true,
			Records: []config.Observation{{Session: "fixture", Sequence: uint64(i + 1), At: f.StartedAt, Kind: "started", Flow: &f}}}); err != nil {
			t.Fatal(err)
		}
	}
	result, err := c.QueryRoutingLibrary(ctx, &emptypb.Empty{})
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, app := range result.Applications {
		if systemID != "" && app.Id == systemID {
			t.Fatal("system application alias offered as user-installed")
		}
		if app.Id == "fixture.user" {
			found = true
		}
		if app.Id == "process:"+tool {
			t.Fatal("standalone tool offered as installed application")
		}
		if app.Id == "process:"+dnsTool || app.Id == "process:/usr/libexec/trustd" || app.Id == "com.apple.trustd" {
			t.Fatalf("non-application leaked into rule candidate: %v", app)
		}
	}
	if !found {
		t.Fatal("installed user bundle missing")
	}
	_, err = c.MutateRoutingLibrary(ctx, &pb.RoutingMutationRequest{ExpectedRevision: result.Revision,
		Action: "save_rule", PackageId: "priority", Rule: &pb.RoutingEntry{Id: "forbidden", Method: "application", Target: "process:" + tool, Destination: "direct", Enabled: true}})
	if err == nil {
		t.Fatal("standalone tool can still be saved as an application")
	}
}
