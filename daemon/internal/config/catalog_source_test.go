package config

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func sourceNode(name, server, uuid string) SourceNode {
	return SourceNode{Name: name, Config: RouteConfig{
		NodeProfile:   NodeProfile{Name: name, ProtocolName: "vless", Server: server, Port: 443},
		SchemaVersion: 1, UUID: uuid, Transport: "ws", Security: "tls", ServerName: server, WSPath: "/ws",
	}}
}

func subscriptionCatalog(t *testing.T) (Catalog, SubscriptionRecord) {
	t.Helper()
	c := NewCatalog()
	record, created, err := c.UpsertSubscription("机场一", "https://sub.example.com/base64?token=secret")
	if err != nil || !created {
		t.Fatalf("subscription: %v %v", created, err)
	}
	return c, record
}

func TestMergeSourceKeepsIDsOverridesAndHiddenRecords(t *testing.T) {
	c, source := subscriptionCatalog(t)
	first := []SourceNode{sourceNode("香港01", "hk1.example.com", "11111111-1111-1111-1111-111111111111"), sourceNode("日本01", "jp1.example.com", "22222222-2222-2222-2222-222222222222")}
	if _, err := c.MergeSource(source.ID, first, true); err != nil {
		t.Fatal(err)
	}
	if len(c.Routes) != 2 {
		t.Fatalf("routes=%d", len(c.Routes))
	}
	hongkong := c.Routes[0]
	if err := c.SetNameOverride(hongkong.Config.ID, hongkong.Revision, "我的香港"); err != nil {
		t.Fatal(err)
	}
	if err := c.Delete(c.Routes[1].Config.ID, ""); err != nil {
		t.Fatal(err)
	}
	if !c.Routes[1].Hidden {
		t.Fatal("subscription delete did not hide")
	}
	before := c.Routes[0]
	revisionBefore := before.Revision

	// 认证轮换但来源身份不变：继续匹配同一记录，保留改名与隐藏。
	rotated := []SourceNode{sourceNode("香港01", "hk1.example.com", "33333333-3333-3333-3333-333333333333"), sourceNode("日本01", "jp1.example.com", "44444444-4444-4444-4444-444444444444")}
	report, err := c.MergeSource(source.ID, rotated, true)
	if err != nil {
		t.Fatal(err)
	}
	if report.Updated != 1 || report.Hidden != 1 || report.Added != 0 || report.Removed != 0 {
		t.Fatalf("report=%+v", report)
	}
	if len(c.Routes) != 2 || c.Routes[0].Config.ID != before.Config.ID {
		t.Fatal("merge replaced the stable route id")
	}
	if c.Routes[0].Config.Name != "我的香港" || c.Routes[0].NameOverride != "我的香港" {
		t.Fatalf("name override lost: %q", c.Routes[0].Config.Name)
	}
	if c.Routes[0].Config.UUID != "33333333-3333-3333-3333-333333333333" {
		t.Fatal("rotated credentials not applied")
	}
	if c.Routes[0].Revision == revisionBefore {
		t.Fatal("connection change did not bump the record revision")
	}
	if !c.Routes[1].Hidden {
		t.Fatal("hidden record was resurrected")
	}
	if err := c.RestoreRoute(c.Routes[1].Config.ID, c.Routes[1].Revision); err != nil {
		t.Fatal(err)
	}
	if c.Routes[1].Hidden || c.Routes[1].SourceRemoved {
		t.Fatal("restore did not clear hidden state")
	}
}

func TestMergeSourceMarksRemovedOnlyForCompleteBodiesAndKeepsPartialOnes(t *testing.T) {
	c, source := subscriptionCatalog(t)
	nodes := []SourceNode{sourceNode("A", "a.example.com", "11111111-1111-1111-1111-111111111111"), sourceNode("B", "b.example.com", "22222222-2222-2222-2222-222222222222")}
	if _, err := c.MergeSource(source.ID, nodes, true); err != nil {
		t.Fatal(err)
	}

	partial, err := c.MergeSource(source.ID, nodes[:1], false)
	if err != nil {
		t.Fatal(err)
	}
	if partial.Kept != 1 || partial.Removed != 0 || !partial.Partial {
		t.Fatalf("partial report=%+v", partial)
	}
	if c.Routes[1].SourceRemoved {
		t.Fatal("partial refresh marked a node removed")
	}

	complete, err := c.MergeSource(source.ID, nodes[:1], true)
	if err != nil {
		t.Fatal(err)
	}
	if complete.Removed != 1 || !c.Routes[1].SourceRemoved {
		t.Fatalf("complete=%+v removed=%v", complete, c.Routes[1].SourceRemoved)
	}
	if len(c.Routes) != 2 {
		t.Fatal("removed node disappeared from the catalog")
	}

	// 节点回来时清除来源移除标记。
	back, err := c.MergeSource(source.ID, nodes, true)
	if err != nil {
		t.Fatal(err)
	}
	if back.Removed != 0 || c.Routes[1].SourceRemoved {
		t.Fatalf("returning node kept removed flag: %+v", back)
	}
}

func TestSourcesStaySeparateAndUnmatchedNodesAreAdded(t *testing.T) {
	c, first := subscriptionCatalog(t)
	second, _, err := c.UpsertSubscription("机场二", "https://other.example.com/list")
	if err != nil {
		t.Fatal(err)
	}
	node := sourceNode("香港01", "hk1.example.com", "11111111-1111-1111-1111-111111111111")
	if _, err := c.MergeSource(first.ID, []SourceNode{node}, true); err != nil {
		t.Fatal(err)
	}
	// 同一配置来自另一来源：各有记录。
	report, err := c.MergeSource(second.ID, []SourceNode{node}, true)
	if err != nil {
		t.Fatal(err)
	}
	if report.Added != 1 || len(c.Routes) != 2 {
		t.Fatalf("cross-source dedup leaked: %+v", report)
	}
	if c.Routes[0].SourceID == c.Routes[1].SourceID {
		t.Fatal("origins not recorded")
	}
	// 同一来源内重复项不新增副本。
	again, err := c.MergeSource(first.ID, []SourceNode{node, node}, true)
	if err != nil {
		t.Fatal(err)
	}
	if again.Added != 0 || len(c.Routes) != 2 {
		t.Fatalf("duplicate inside one source added a copy: %+v", again)
	}
	// 无法识别的节点保留并计入 Kept（部分正文）而不是假装删除。
	unknown := sourceNode("香港02", "hk2.example.com", "55555555-5555-5555-5555-555555555555")
	kept, err := c.MergeSource(first.ID, []SourceNode{unknown}, false)
	if err != nil {
		t.Fatal(err)
	}
	if kept.Added != 1 || kept.Kept != 1 || len(c.Routes) != 3 {
		t.Fatalf("unrecognized node handling: %+v routes=%d", kept, len(c.Routes))
	}
}

func TestCatalogStoreRoundTripsSubscriptionsAndRejectsMalformedRecords(t *testing.T) {
	directory, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(directory, 0700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(directory, "routes.json")
	store := NewCatalogStore(path, uint32(os.Getuid()))
	catalog, source := subscriptionCatalog(t)
	if _, err := catalog.MergeSource(source.ID, []SourceNode{sourceNode("A", "a.example.com", "11111111-1111-1111-1111-111111111111")}, true); err != nil {
		t.Fatal(err)
	}
	if err := store.Save(context.Background(), catalog); err != nil {
		t.Fatal(err)
	}
	loaded, exists, err := store.Load()
	if err != nil || !exists {
		t.Fatalf("load: %v %v", exists, err)
	}
	if len(loaded.Subscriptions) != 1 || len(loaded.Routes) != 1 || loaded.Routes[0].SourceID != source.ID {
		t.Fatalf("round trip lost source mapping: %+v", loaded)
	}

	// 本地线路不得携带来源字段；订阅线路必须指向已保存来源。
	broken := catalog.Clone()
	broken.Routes[0].SourceID = "missing"
	if err := store.Save(context.Background(), broken); err == nil {
		t.Fatal("Save wrote a catalog with a dangling source mapping")
	}
	reloaded, _, err := store.Load()
	if err != nil || len(reloaded.Routes) != 1 || reloaded.Routes[0].SourceID != source.ID {
		t.Fatalf("rejected save damaged the stored catalog: %v", err)
	}
}
