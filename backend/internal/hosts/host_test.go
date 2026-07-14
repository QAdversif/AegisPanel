// SPDX-License-Identifier: AGPL-3.0-or-later

package hosts

import (
	"errors"
	"strings"
	"testing"

	"github.com/google/uuid"
)

func TestHost_IsValid_AcceptsDirectSingleEndpoint(t *testing.T) {
	h := &Host{
		ID:       uuid.New(),
		Remark:   "Latvia",
		Type:     HostTypeDirect,
		Enabled:  true,
		Priority: 0,
		Endpoints: []Endpoint{
			{ID: uuid.New(), NodeID: uuid.New(), InboundID: uuid.New(), Weight: 1},
		},
	}
	if !h.IsValid() {
		t.Fatalf("direct host with 1 endpoint should be valid")
	}
}

func TestHost_IsValid_AcceptsBalancerMultiEndpoint(t *testing.T) {
	h := &Host{
		ID:       uuid.New(),
		Remark:   "Premium EU",
		Type:     HostTypeBalancer,
		Enabled:  true,
		Priority: 0,
		Endpoints: []Endpoint{
			{ID: uuid.New(), NodeID: uuid.New(), InboundID: uuid.New(), Weight: 1},
			{ID: uuid.New(), NodeID: uuid.New(), InboundID: uuid.New(), Weight: 1},
		},
		Balancer: &Balancer{Strategy: StrategyRoundRobin},
	}
	if !h.IsValid() {
		t.Fatalf("balancer host with 2 endpoints should be valid")
	}
}

func TestHost_IsValid_RejectsEmptyRemark(t *testing.T) {
	h := &Host{
		ID:     uuid.New(),
		Type:   HostTypeDirect,
		Remark: "",
		Endpoints: []Endpoint{
			{ID: uuid.New(), NodeID: uuid.New(), InboundID: uuid.New(), Weight: 1},
		},
	}
	if h.IsValid() {
		t.Fatal("empty remark should be invalid")
	}
}

func TestHost_IsValid_RejectsUnknownType(t *testing.T) {
	h := &Host{
		ID:     uuid.New(),
		Remark: "x",
		Type:   HostType("chain"),
		Endpoints: []Endpoint{
			{ID: uuid.New(), NodeID: uuid.New(), InboundID: uuid.New(), Weight: 1},
		},
	}
	if h.IsValid() {
		t.Fatal("unknown type should be invalid")
	}
}

func TestHost_IsValid_RejectsEmptyEndpoints(t *testing.T) {
	h := &Host{
		ID:        uuid.New(),
		Remark:    "x",
		Type:      HostTypeDirect,
		Endpoints: nil,
	}
	if h.IsValid() {
		t.Fatal("empty endpoints should be invalid")
	}
}

func TestHost_IsValid_RejectsEndpointWithZeroNodeID(t *testing.T) {
	h := &Host{
		ID:     uuid.New(),
		Remark: "x",
		Type:   HostTypeDirect,
		Endpoints: []Endpoint{
			{ID: uuid.New(), NodeID: uuid.Nil, InboundID: uuid.New(), Weight: 1},
		},
	}
	if h.IsValid() {
		t.Fatal("zero node_id should be invalid")
	}
}

func TestHost_IsValid_RejectsEndpointWithZeroInboundID(t *testing.T) {
	h := &Host{
		ID:     uuid.New(),
		Remark: "x",
		Type:   HostTypeDirect,
		Endpoints: []Endpoint{
			{ID: uuid.New(), NodeID: uuid.New(), InboundID: uuid.Nil, Weight: 1},
		},
	}
	if h.IsValid() {
		t.Fatal("zero inbound_id should be invalid")
	}
}

func TestHost_IsValid_RejectsNegativeWeight(t *testing.T) {
	h := &Host{
		ID:     uuid.New(),
		Remark: "x",
		Type:   HostTypeDirect,
		Endpoints: []Endpoint{
			{ID: uuid.New(), NodeID: uuid.New(), InboundID: uuid.New(), Weight: -1},
		},
	}
	if h.IsValid() {
		t.Fatal("negative weight should be invalid")
	}
}

func TestEndpoint_Clone_IsDeepEnough(t *testing.T) {
	src := Endpoint{
		ID:        uuid.New(),
		NodeID:    uuid.New(),
		InboundID: uuid.New(),
		Weight:    1,
		Address:   []string{"a", "b"},
		SNI:       []string{"s"},
		Host:      []string{"h"},
		Port:      ptrInt(443),
		Path:      "/ws",
	}
	dst := cloneEndpoint(src)
	// Mutate dst's slices; src must not change.
	dst.Address[0] = "MUTATED"
	dst.SNI = append(dst.SNI, "X")
	dst.Host[0] = "MUTATED"
	*dst.Port = 999
	if src.Address[0] != "a" {
		t.Errorf("Address clone is shallow: %v", src.Address)
	}
	if len(src.SNI) != 1 {
		t.Errorf("SNI clone is shallow: %v", src.SNI)
	}
	if src.Host[0] != "h" {
		t.Errorf("Host clone is shallow: %v", src.Host)
	}
	if *src.Port != 443 {
		t.Errorf("Port clone is shallow: %d", *src.Port)
	}
}

func TestHost_Clone_IsDeepEnough(t *testing.T) {
	src := &Host{
		ID:           uuid.New(),
		Remark:       "x",
		Type:         HostTypeBalancer,
		Enabled:      true,
		StatusFilter: []UserStatus{UserStatusActive, UserStatusOnHold},
		Tags:         []string{"eu", "premium"},
		Endpoints: []Endpoint{
			{ID: uuid.New(), NodeID: uuid.New(), InboundID: uuid.New(), Weight: 1},
		},
		Balancer: &Balancer{
			Strategy:            StrategyRoundRobin,
			FailoverEndpointIDs: []uuid.UUID{uuid.New()},
		},
	}
	dst := cloneHost(src)
	dst.StatusFilter[0] = UserStatusDisabled
	dst.Tags[0] = "MUTATED"
	dst.Endpoints[0].Address = []string{"MUTATED"}
	dst.Balancer.FailoverEndpointIDs[0] = uuid.Nil
	dst.Balancer.Strategy = StrategyRandom
	if src.StatusFilter[0] != UserStatusActive {
		t.Errorf("StatusFilter clone is shallow")
	}
	if src.Tags[0] != "eu" {
		t.Errorf("Tags clone is shallow")
	}
	if len(src.Endpoints[0].Address) != 0 {
		t.Errorf("Endpoints clone is shallow: %v", src.Endpoints[0].Address)
	}
	if src.Balancer.Strategy != StrategyRoundRobin {
		t.Errorf("Balancer clone is shallow")
	}
	if src.Balancer.FailoverEndpointIDs[0] == uuid.Nil {
		t.Errorf("FailoverEndpointIDs clone is shallow")
	}
}

func TestValidationError_ErrorMentionsField(t *testing.T) {
	e := &ValidationError{Field: "remark", Message: "must not be empty"}
	if got := e.Error(); !strings.Contains(got, "remark") || !strings.Contains(got, "must not be empty") {
		t.Fatalf("error message %q missing field/message", got)
	}
}

func TestValidationError_UnwrapsToErrInvalid(t *testing.T) {
	e := &ValidationError{Field: "remark", Message: "x"}
	if got := e.Unwrap(); !errors.Is(e, ErrInvalid) {
		t.Fatalf("Unwrap = %v, want ErrInvalid (via errors.Is)", got)
	}
}

func ptrInt(v int) *int { return &v }
