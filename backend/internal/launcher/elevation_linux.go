//go:build linux

package launcher

import (
	"os/user"
	"strconv"

	"webfile/internal/identity"
)

// ElevationAllowed requires explicit membership in a configured administrative
// group. An empty policy or any unresolvable group denies elevation.
func ElevationAllowed(id identity.Identity, groups []string) bool {
	allowed := false
	for _, name := range groups {
		group, err := user.LookupGroup(name)
		if err != nil {
			return false
		}
		gid, err := strconv.ParseUint(group.Gid, 10, 32)
		if err != nil {
			return false
		}
		for _, member := range id.Groups {
			if uint32(gid) == member {
				allowed = true
			}
		}
	}
	return allowed
}
