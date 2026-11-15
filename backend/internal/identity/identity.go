package identity

import (
	"fmt"
	"math"
	"os/user"
	"strconv"
)

type Identity struct {
	Username string   `json:"username"`
	UID      uint32   `json:"uid"`
	GID      uint32   `json:"gid"`
	Groups   []uint32 `json:"-"`
	Home     string   `json:"home"`
}

// OwnershipIDs converts Linux IDs to the native int expected by os.Chown.
// Linux reserves the all-ones ID to mean "leave ownership unchanged".
func (id Identity) OwnershipIDs() (int, int, error) {
	if id.UID == math.MaxUint32 || id.GID == math.MaxUint32 {
		return 0, 0, fmt.Errorf("reserved filesystem ownership ID")
	}
	uid, err := strconv.Atoi(strconv.FormatUint(uint64(id.UID), 10))
	if err != nil {
		return 0, 0, fmt.Errorf("uid exceeds native ownership range: %w", err)
	}
	gid, err := strconv.Atoi(strconv.FormatUint(uint64(id.GID), 10))
	if err != nil {
		return 0, 0, fmt.Errorf("gid exceeds native ownership range: %w", err)
	}
	return uid, gid, nil
}

func Lookup(username string) (Identity, error) {
	u, err := user.Lookup(username)
	if err != nil {
		return Identity{}, err
	}
	uid, err := parseID(u.Uid)
	if err != nil {
		return Identity{}, fmt.Errorf("parse uid: %w", err)
	}
	gid, err := parseID(u.Gid)
	if err != nil {
		return Identity{}, fmt.Errorf("parse gid: %w", err)
	}
	groupStrings, err := u.GroupIds()
	if err != nil {
		return Identity{}, fmt.Errorf("list groups: %w", err)
	}
	groups := make([]uint32, 0, len(groupStrings))
	seen := map[uint32]struct{}{}
	for _, raw := range groupStrings {
		id, err := parseID(raw)
		if err != nil {
			continue
		}
		if _, ok := seen[id]; ok {
			continue
		}
		seen[id] = struct{}{}
		groups = append(groups, id)
	}
	if _, ok := seen[gid]; !ok {
		groups = append(groups, gid)
	}
	return Identity{
		Username: u.Username,
		UID:      uid,
		GID:      gid,
		Groups:   groups,
		Home:     u.HomeDir,
	}, nil
}

func Current() (Identity, error) {
	u, err := user.Current()
	if err != nil {
		return Identity{}, err
	}
	return Lookup(u.Username)
}

func parseID(value string) (uint32, error) {
	parsed, err := strconv.ParseUint(value, 10, 32)
	return uint32(parsed), err
}
