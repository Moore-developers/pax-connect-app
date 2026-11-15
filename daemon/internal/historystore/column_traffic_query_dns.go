package historystore

import (
	"database/sql/driver"
	"fmt"

	"modernc.org/sqlite"
	"pax-connect/daemon/internal/config"
)

func init() {
	sqlite.MustRegisterDeterministicScalarFunction("pax_dns_group", 3, func(_ *sqlite.FunctionContext, a []driver.Value) (driver.Value, error) {
		v := make([]string, 3)
		for i := range a {
			if a[i] != nil {
				v[i] = fmt.Sprint(a[i])
			}
		}
		return config.TrafficDNSGroupID(v[0], v[1], v[2]), nil
	})
}
