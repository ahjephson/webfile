package identity

import (
	"fmt"
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
