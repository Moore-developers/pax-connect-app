package singbox

import (
	"bytes"
	"context"
	stdjson "encoding/json"
	"errors"
	"io"
	"net/netip"
	"path/filepath"
	"reflect"
	"strings"

	"github.com/sagernet/sing-box/include"
	"github.com/sagernet/sing-box/option"
	"github.com/sagernet/sing/common/json"
	"github.com/sagernet/sing/service"
)

const coreProfileLimit = 4 << 20

func coreInvalid() error { return errors.New("core_profile_invalid") }

// ValidateCoreOptions uses the same registry-aware decoder as StartedService.
// Options and union decoders use DisallowUnknownFields. That is insufficient:
// Go accepts case-insensitive field names and duplicate keys, and a few custom
// decoders ignore unknown fields. The token pass rejects duplicate/escaped keys;
// the typed schema pass checks exact JSON tags (including custom union fields).
// Policy below is a positive list of reviewed typed fields, never a key denylist.
// Only the re-marshalled typed value, after core rewrites, crosses into runtime.
func ValidateCoreOptions(ctx context.Context, raw []byte, state string, mixedPort int) (string, error) {
	if len(raw) > coreProfileLimit {
		return "", errors.New("core_profile_too_large")
	}
	d := stdjson.NewDecoder(bytes.NewReader(raw))
	d.UseNumber()
	doc, err := coreJSONValue(d, raw, 0)
	if err != nil {
		return "", coreInvalid()
	}
	if _, err = d.Token(); err != io.EOF {
		return "", coreInvalid()
	}
	if _, ok := doc.(map[string]any); !ok {
		return "", coreInvalid()
	}
	ctx = service.ContextWithDefaultRegistry(include.Context(ctx))
	opts, err := json.UnmarshalExtendedContext[option.Options](ctx, raw)
	if err != nil {
		return "", coreInvalid()
	}
	if !coreExactKeys(doc, reflect.ValueOf(opts)) {
		return "", coreInvalid()
	}
	if err = coreOptionsPolicy(&opts, state, mixedPort); err != nil {
		return "", err
	}
	data, err := json.MarshalContext(ctx, opts)
	if err != nil {
		return "", coreInvalid()
	}
	return string(data), nil
}

func coreJSONValue(d *stdjson.Decoder, raw []byte, depth int) (any, error) {
	if depth > 64 {
		return nil, coreInvalid()
	}
	token, err := d.Token()
	if err != nil {
		return nil, err
	}
	delim, ok := token.(stdjson.Delim)
	if !ok {
		return token, nil
	}
	switch delim {
	case '{':
		object := map[string]any{}
		for d.More() {
			start := d.InputOffset()
			keyToken, e := d.Token()
			if e != nil {
				return nil, e
			}
			key, ok := keyToken.(string)
			if !ok || bytes.Contains(raw[start:d.InputOffset()], []byte(`\`)) {
				return nil, coreInvalid()
			}
			if _, duplicate := object[key]; duplicate {
				return nil, coreInvalid()
			}
			value, e := coreJSONValue(d, raw, depth+1)
			if e != nil {
				return nil, e
			}
			object[key] = value
		}
		_, err = d.Token()
		return object, err
	case '[':
		var values []any
		for d.More() {
			v, e := coreJSONValue(d, raw, depth+1)
			if e != nil {
				return nil, e
			}
			values = append(values, v)
		}
		_, err = d.Token()
		return values, err
	}
	return nil, coreInvalid()
}

// coreExactKeys also covers zero-valued/omitted fields: comparing marshalled
// maps alone would reject legitimate defaults, Listable strings and durations.
func coreExactKeys(raw any, v reflect.Value) bool {
	if raw == nil {
		return true
	}
	for v.IsValid() && (v.Kind() == reflect.Pointer || v.Kind() == reflect.Interface) {
		if v.IsNil() {
			v = reflect.Zero(v.Type().Elem())
			break
		}
		v = v.Elem()
	}
	if !v.IsValid() {
		return false
	}
	switch r := raw.(type) {
	case map[string]any:
		if v.Kind() == reflect.Map {
			return true
		} // headers: data keys, not option fields
		if v.Kind() != reflect.Struct {
			return false
		}
		// badjson.TypedMap has a custom data-key representation.
		if strings.Contains(v.Type().PkgPath(), "/badjson") {
			return true
		}
		fields := map[string]reflect.Value{}
		coreJSONFields(v, fields)
		for k, value := range r {
			field, ok := fields[k]
			if !ok || !coreExactKeys(value, field) {
				return false
			}
		}
	case []any:
		if v.Kind() != reflect.Slice && v.Kind() != reflect.Array {
			return false
		}
		for i, value := range r {
			elem := reflect.Zero(v.Type().Elem())
			if i < v.Len() {
				elem = v.Index(i)
			}
			if !coreExactKeys(value, elem) {
				return false
			}
		}
	}
	return true
}
func coreJSONFields(v reflect.Value, fields map[string]reflect.Value) {
	for i := 0; i < v.NumField(); i++ {
		sf, f := v.Type().Field(i), v.Field(i)
		if !sf.IsExported() {
			continue
		}
		// Exact keys and field presence both obey the static allowlist. An upstream
		// addition is rejected even if its supplied value happens to be zero.
		allowed := coreAllowedFields[v.Type().Name()]
		if !strings.Contains(" "+allowed+" ", " "+sf.Name+" ") {
			continue
		}
		name := strings.Split(sf.Tag.Get("json"), ",")[0]
		if sf.Anonymous || name == "-" {
			for f.Kind() == reflect.Pointer || f.Kind() == reflect.Interface {
				if f.IsNil() {
					f = reflect.Value{}
					break
				}
				f = f.Elem()
			}
			if f.IsValid() && f.Kind() == reflect.Struct && f.Type().PkgPath() == reflect.TypeFor[option.Options]().PkgPath() {
				coreJSONFields(f, fields)
			}
		} else if name != "" {
			fields[name] = f
		}
	}
}

func coreOptionsPolicy(o *option.Options, state string, mixedPort int) error {
	fail := func(code string) error { return errors.New(code) }
	if len(o.Endpoints) != 0 || len(o.Services) != 0 || len(o.CertificateProviders) != 0 {
		return fail("core_component_forbidden")
	}
	systemProxyCount := 0
	for _, in := range o.Inbounds {
		switch in.Type {
		case "tun":
		case "mixed":
			m, ok := in.Options.(*option.HTTPMixedInboundOptions)
			if !ok || m.Listen == nil {
				return fail("core_mixed_loopback_required")
			}
			addr := m.Listen.Build(netip.Addr{})
			if addr != netip.MustParseAddr("127.0.0.1") && addr != netip.IPv6Loopback() {
				return fail("core_mixed_loopback_required")
			}
			if m.SetSystemProxy {
				systemProxyCount++
				// Startup cleanup recognizes exactly this IPv4 endpoint. An IPv6
				// system proxy would otherwise survive an abnormal core exit.
				if systemProxyCount > 1 || mixedPort <= 0 || mixedPort > 65535 || int(m.ListenPort) != mixedPort || addr != netip.MustParseAddr("127.0.0.1") {
					return fail("core_system_proxy_forbidden")
				}
			}
		default:
			return fail("core_inbound_forbidden")
		}
	}
	for _, out := range o.Outbounds {
		// Importers/builders emit these six protocols; socks/http are explicitly included by
		// the core contract. No TUIC/WireGuard builder exists. Never allow tor/ssh.
		switch out.Type {
		case "direct", "block", "selector", "urltest", "vmess", "vless", "trojan", "shadowsocks", "hysteria2", "anytls", "socks", "http":
		default:
			return fail("core_outbound_forbidden")
		}
	}
	if o.DNS != nil {
		for _, s := range o.DNS.Servers {
			switch s.Type {
			case "udp", "https":
			case "local":
				local, ok := s.Options.(*option.LocalDNSServerOptions)
				if s.Tag != "dns-intranet" || !ok || !reflect.ValueOf(*local).IsZero() {
					return fail("core_dns_server_forbidden")
				}
			default:
				return fail("core_dns_server_forbidden")
			}
		}
	}
	if o.Log != nil && o.Log.Output != "" {
		return fail("core_log_output_forbidden")
	}
	if o.Experimental != nil {
		e := o.Experimental
		if e.Debug != nil && !reflect.ValueOf(*e.Debug).IsZero() {
			return fail("core_debug_forbidden")
		}
		if e.V2RayAPI != nil && !reflect.ValueOf(*e.V2RayAPI).IsZero() {
			return fail("core_v2ray_api_forbidden")
		}
		e.Debug = nil
		e.V2RayAPI = nil
		if c := e.ClashAPI; c != nil {
			//nolint:staticcheck // Legacy cache_file remains a root file capability; reject it.
			if c.ExternalController != "" || c.ExternalUI != "" || c.ExternalUIDownloadURL != "" || c.CacheFile != "" {
				return fail("core_clash_api_forbidden")
			}
			e.ClashAPI = nil // clear secret and all unneeded legacy/HTTP fields
		}
		if c := e.CacheFile; c != nil && c.Path != "" && c.Path != filepath.Join(state, "cache.db") {
			return fail("core_cache_path_forbidden")
		}
	}
	if o.Route != nil {
		for i := range o.Route.RuleSet {
			r := &o.Route.RuleSet[i]
			if r.Type != "local" || r.Format != "binary" || (len(r.Tag) != 1 || (r.Tag[0] != "geosite-cn" && r.Tag[0] != "geoip-cn" && r.Tag[0] != "geosite-google")) {
				return fail("core_ruleset_forbidden")
			}
			r.LocalOptions.Path = filepath.Join(state, "rulesets", r.Tag[0]+".srs")
		}
	}
	// Check before inserting core-owned paths. No unlisted nonzero typed field
	// survives, including fields added by a future upstream version.
	if !coreAllowedValue(reflect.ValueOf(*o)) {
		return fail("core_field_forbidden")
	}
	if o.Log == nil {
		o.Log = &option.LogOptions{}
	}
	// StartedService receives all levels through its platform writer. Use a
	// fixed OS discard device for its independent default writer, never stderr
	// or a caller-chosen file, and clamp the public level to info..error.
	switch o.Log.Level {
	case "warn", "error":
	case "fatal", "panic":
		o.Log.Level = "error"
	default:
		o.Log.Level = "info"
	}
	o.Log.Output = "/dev/null"
	o.Log.Disabled = false
	if o.Experimental == nil {
		o.Experimental = &option.ExperimentalOptions{}
	}
	o.Experimental.CacheFile = &option.CacheFileOptions{Enabled: true, Path: filepath.Join(state, "cache.db")}
	return nil
}

func coreAllowedValue(v reflect.Value) bool {
	if !v.IsValid() || v.IsZero() {
		return true
	}
	switch v.Kind() {
	case reflect.Pointer, reflect.Interface:
		return coreAllowedValue(v.Elem())
	case reflect.Slice, reflect.Array:
		for i := 0; i < v.Len(); i++ {
			if !coreAllowedValue(v.Index(i)) {
				return false
			}
		}
		return true
	case reflect.Map:
		return true // only explicitly admitted typed header/data maps
	case reflect.Struct:
		if v.Type().PkgPath() != reflect.TypeFor[option.Options]().PkgPath() {
			return true
		} // netip, badoption scalar/data wrappers
		allowed, ok := coreAllowedFields[v.Type().Name()]
		if !ok {
			return false
		}
		for i := 0; i < v.NumField(); i++ {
			sf := v.Type().Field(i)
			if !sf.IsExported() || sf.Name == "RawMessage" || sf.Name == "CommentsSet" {
				continue
			}
			field := v.Field(i)
			if field.IsZero() {
				continue
			}
			if !strings.Contains(" "+allowed+" ", " "+sf.Name+" ") || !coreAllowedValue(field) {
				return false
			}
		}
	}
	return true
}
