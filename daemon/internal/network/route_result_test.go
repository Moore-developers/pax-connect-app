package network

import (
	"errors"
	"testing"
)

func TestRouteCommandResultDistinguishesMissingRoute(t *testing.T) {
	for _, tc := range []struct {
		name, out, stderr string
		commandErr, want  error
	}{
		{"macOS zero exit absent", "", "route: writing to routing socket: not in table\n", nil, ErrNoRoute},
		{"nonzero absent", "", "route: writing to routing socket: not in table\n", errors.New("exit"), ErrNoRoute},
		{"found", "  interface: en0\n", "", nil, nil},
		{"empty success", "", "", nil, errObservationUnavailable},
		{"permission", "", "permission denied", errors.New("exit"), errObservationUnavailable},
		{"cancelled", "", "", errors.New("killed"), errObservationUnavailable},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := routeCommandResult([]byte(tc.out), []byte(tc.stderr), tc.commandErr)
			if !errors.Is(err, tc.want) {
				t.Fatalf("got %v want %v", err, tc.want)
			}
		})
	}
}
