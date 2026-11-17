package launcher

import (
	"encoding/json"
	"net"
	"os/user"
	"strconv"
	"strings"
	"testing"

	"webfile/internal/identity"
)

func TestGIVEN_GroupPolicy_WHEN_AuthorizingElevation_THEN_RequiresExplicitMembership(t *testing.T) {
	current, err := user.Current()
	if err != nil {
		t.Fatal(err)
	}
	group, err := user.LookupGroupId(current.Gid)
	if err != nil {
		t.Fatal(err)
	}
	gid, err := strconv.ParseUint(group.Gid, 10, 32)
	if err != nil {
		t.Fatal(err)
	}
	id := identity.Identity{UID: 1000, Groups: []uint32{uint32(gid)}}
	for _, scenario := range []struct {
		name     string
		groups   []string
		identity identity.Identity
		allowed  bool
	}{
		{"disabled", nil, id, false},
		{"member", []string{group.Name}, id, true},
		{"nonmember", []string{group.Name}, identity.Identity{UID: 1001}, false},
		{"missing-policy-group", []string{group.Name, "webfile-nonexistent-policy-group"}, id, false},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			if got := ElevationAllowed(scenario.identity, scenario.groups); got != scenario.allowed {
				t.Fatalf("authorization = %v, expected %v", got, scenario.allowed)
			}
		})
	}
}

func TestGIVEN_OrdinaryAccount_WHEN_RequestingRootWorker_THEN_LauncherDeniesWithoutPolicy(t *testing.T) {
	current, err := user.Current()
	if err != nil {
		t.Fatal(err)
	}
	server := &Server{PAMService: "webfile", MinUserUID: 1}
	client, peer := net.Pipe()
	defer client.Close()
	go server.handle(peer, 0)
	err = json.NewEncoder(client).Encode(Request{Action: "elevate", Username: current.Username, Password: "Password", CacheToken: strings.Repeat("a", 64), ElevationToken: strings.Repeat("b", 64)})
	if err != nil {
		t.Fatal(err)
	}
	var response Response
	if err := json.NewDecoder(client).Decode(&response); err != nil {
		t.Fatal(err)
	}
	if response.OK || response.Socket != "" || response.Error != "user is not allowed to elevate filesystem access" {
		t.Fatalf("unexpected elevation response: %#v", response)
	}
}
