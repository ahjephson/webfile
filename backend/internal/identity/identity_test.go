package identity

import (
	"math"
	"strconv"
	"testing"
)

func TestOwnershipIDsPreserveNativeRange(t *testing.T) {
	for _, value := range []uint32{0, 1000, math.MaxInt32, math.MaxInt32 + 1, math.MaxUint32 - 1} {
		uid, gid, err := (Identity{UID: value, GID: value}).OwnershipIDs()
		if strconv.IntSize == 32 && value > math.MaxInt32 {
			if err == nil {
				t.Fatalf("out-of-range ID %d was accepted on a 32-bit platform", value)
			}
			continue
		}
		if err != nil || uint64(uid) != uint64(value) || uint64(gid) != uint64(value) {
			t.Fatalf("ownership ID %d changed: uid=%d gid=%d err=%v", value, uid, gid, err)
		}
	}
}

func TestOwnershipIDsRejectUnchangedSentinel(t *testing.T) {
	for _, id := range []Identity{{UID: math.MaxUint32, GID: 1000}, {UID: 1000, GID: math.MaxUint32}} {
		if _, _, err := id.OwnershipIDs(); err == nil {
			t.Fatalf("reserved ownership ID accepted: %#v", id)
		}
	}
}
