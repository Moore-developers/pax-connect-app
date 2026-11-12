package settings

import (
	E "github.com/sagernet/sing/common/exceptions"
	"github.com/sagernet/sing/common/shell"
	"strconv"
	"strings"
)

// This method replaces only DarwinSystemProxy.disableLocked in the pinned upstream
// file. It clears the endpoint currently owned by this instance; it never
// restores values from before Enable or re-enables another application's proxy.
func (p *DarwinSystemProxy) disableLocked() error {
	interfaceDisplayName, err := getInterfaceDisplayName(p.interfaceName)
	if err != nil {
		return err
	}
	kinds := []string{"webproxy", "securewebproxy"}
	if p.supportSOCKS {
		kinds = append([]string{"socksfirewallproxy"}, kinds...)
	}
	for _, kind := range kinds {
		raw, err := shell.Exec("networksetup", "-get"+kind, interfaceDisplayName).ReadOutput()
		if err != nil {
			return E.New("system proxy observation unavailable")
		}
		values := make(map[string]string)
		for _, line := range strings.Split(string(raw), "\n") {
			key, value, ok := strings.Cut(line, ":")
			if ok {
				key = strings.TrimSpace(key)
				if _, duplicate := values[key]; duplicate {
					return E.New("system proxy observation unavailable")
				}
				values[key] = strings.TrimSpace(value)
			}
		}
		if values["Enabled"] == "No" {
			continue
		}
		port, portErr := strconv.Atoi(values["Port"])
		if values["Enabled"] != "Yes" || values["Server"] == "" || portErr != nil || port <= 0 || port > 65535 {
			return E.New("system proxy observation unavailable")
		}
		if values["Server"] != p.serverAddr.AddrString() || port != int(p.serverAddr.Port) {
			continue
		}
		if err := shell.Exec("networksetup", "-set"+kind+"state", interfaceDisplayName, "off").Attach().Run(); err != nil {
			return err
		}
	}
	p.isEnabled = false
	return nil
}
