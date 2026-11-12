package config

import "testing"

func TestDebugProfileRejectsUnknownFieldsAndMissingCredentials(t *testing.T) {
	for _, body := range []string{
		`{"schema_version":1,"id":"debug","name":"调试线路","protocol":"hysteria2","server":"test.invalid","port":443,"password":"secret","insecure":true}`,
		`{"schema_version":1,"id":"debug","name":"调试线路","protocol":"hysteria2","server":"test.invalid","port":443}`,
	} {
		if _, err := ParseDebugProfile([]byte(body)); err == nil {
			t.Fatal("unsafe profile accepted")
		}
	}
}
func TestDebugProfileHasNoCredentialInPublicModel(t *testing.T) {
	profile, err := ParseDebugProfile([]byte(`{"schema_version":1,"id":"debug","name":"调试线路","protocol":"hysteria2","server":"test.invalid","port":443,"password":"secret","server_name":"test.invalid"}`))
	if err != nil {
		t.Fatal(err)
	}
	if profile.ID != "debug" || profile.ProtocolName != "hysteria2" {
		t.Fatal("incorrect public profile")
	}
}
