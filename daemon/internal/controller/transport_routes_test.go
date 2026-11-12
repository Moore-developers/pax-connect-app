package controller

import (
	"context"
	"encoding/base64"
	"encoding/json"
	paxv1 "pax-connect/daemon/gen/proto/paxv1"
	"strings"
	"testing"
)

func TestWebTransportMatrixPreservesHostPathAndSNI(t *testing.T) {
	for _, protocol := range []string{"vmess", "vless", "trojan"} {
		for _, transport := range []string{"ws", "httpupgrade"} {
			t.Run(protocol+"/"+transport, func(t *testing.T) {
				c, _ := newSubscriptionController(t, &fakeFetcher{})
				ctx := context.Background()
				results, stop := operationSink(c)
				defer stop()
				link := protocol + "://" + uuidA + "@edge.example.test:443?security=tls&type=" + transport + "&host=host.example.test&sni=sni.example.test&path=%2Ftransport#Web"
				if protocol == "vmess" {
					link = "vmess://" + base64.RawStdEncoding.EncodeToString([]byte(`{"v":"2","ps":"Web","add":"edge.example.test","port":"443","id":"`+uuidA+`","aid":"0","net":"`+transport+`","host":"host.example.test","path":"/transport","tls":"tls","sni":"sni.example.test"}`))
				}
				preview, err := c.PreviewRoutes(ctx, &paxv1.PreviewRoutesRequest{Source: link})
				if err != nil {
					t.Fatal(err)
				}
				if len(preview.Items) != 1 || preview.Items[0].Route == nil || preview.Items[0].ErrorCode != "" {
					t.Fatal("matrix shape rejected", preview)
				}
				ack, err := c.ConfirmImportRoutes(ctx, &paxv1.ConfirmImportRoutesRequest{Token: preview.Token, ExpectedCatalogRevision: preview.CatalogRevision, ItemIds: []string{preview.Items[0].ItemId}})
				if err != nil {
					t.Fatal(err)
				}
				if !awaitResult(t, results, ack.OperationId).Ok {
					t.Fatal("import failed")
				}
				node := waitComplete(t, c).Routes[0]
				editor, err := c.GetRouteEditor(ctx, &paxv1.GetRouteEditorRequest{RouteId: node.Id})
				if err != nil {
					t.Fatal(err)
				}
				if editor.Fields.WsHost != "host.example.test" || editor.Fields.WsPath != "/transport" || editor.Fields.ServerName != "sni.example.test" {
					t.Fatal("projection lost independent host/path/SNI")
				}
				var document map[string]any
				if json.Unmarshal([]byte(editor.DocumentJson), &document) != nil {
					t.Fatal("invalid document")
				}
				tr := document["transport"].(map[string]any)
				if transport == "httpupgrade" && tr["host"] != "host.example.test" {
					t.Fatal("HTTPUpgrade host incorrectly mapped")
				}
				changed := strings.Replace(editor.DocumentJson, "host.example.test", "changed.example.test", 1)
				ack, err = c.UpdateRoute(ctx, &paxv1.RouteEditRequest{RouteId: node.Id, ExpectedRevision: node.Revision, DocumentJson: changed})
				if err != nil {
					t.Fatal(err)
				}
				if !awaitResult(t, results, ack.OperationId).Ok {
					t.Fatal("edit failed")
				}
				editor, err = c.GetRouteEditor(ctx, &paxv1.GetRouteEditorRequest{RouteId: node.Id})
				if err != nil {
					t.Fatal(err)
				}
				if editor.Fields.WsHost != "changed.example.test" || editor.Fields.ServerName != "sni.example.test" {
					t.Fatal("host edit changed SNI")
				}
			})
		}
	}
}

func TestTransportMatrixRejectsXHTTPAndHY2WebTransport(t *testing.T) {
	c, _ := newSubscriptionController(t, &fakeFetcher{})
	ctx := context.Background()
	results, stop := operationSink(c)
	defer stop()
	for _, raw := range []string{
		`{"type":"hysteria2","tag":"Invalid","server":"example.test","server_port":443,"password":"fixture","tls":{"enabled":true,"server_name":"example.test"},"transport":{"type":"ws","path":"/"}}`,
		`{"type":"vless","tag":"Invalid","server":"example.test","server_port":443,"uuid":"11111111-1111-1111-1111-111111111111","tls":{"enabled":true,"server_name":"example.test"},"transport":{"type":"xhttp","path":"/"}}`,
	} {
		ack, err := c.AddRoute(ctx, &paxv1.RouteEditRequest{DocumentJson: raw})
		if err != nil {
			continue
		}
		if awaitResult(t, results, ack.OperationId).Ok {
			t.Fatal("unsupported transport admitted")
		}
	}
	if len(waitComplete(t, c).Routes) != 0 {
		t.Fatal("invalid document wrote")
	}
}
