package core

import "context"

const maxProfileBytes = 4 << 20

// ProfileValidator admits user options and returns only canonical, core-owned
// bytes using the launch state directory and mixed port. Implementations keep upstream option types outside this package.
type ProfileValidator func(context.Context, []byte, string, int) (string, error)
