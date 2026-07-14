// SPDX-License-Identifier: AGPL-3.0-or-later

package inbounds

import (
	"errors"
	"testing"

	"github.com/google/uuid"
)

func TestInbound_IsValid_AcceptsMinimal(t *testing.T) {
	i := &Inbound{
		ID:         uuid.New(),
		NodeID:     uuid.New(),
		Name:       "vless-main",
		Protocol:   ProtocolVLESS,
		Listen:     "::",
		ListenPort: 443,
		Enabled:    true,
	}
	if !i.IsValid() {
		t.Fatal("minimal valid inbound should pass IsValid")
	}
}

func TestInbound_IsValid_RejectsEmptyName(t *testing.T) {
	i := &Inbound{
		ID:         uuid.New(),
		NodeID:     uuid.New(),
		Name:       "",
		Protocol:   ProtocolVLESS,
		Listen:     "::",
		ListenPort: 443,
	}
	if i.IsValid() {
		t.Fatal("empty name should be invalid")
	}
}

func TestInbound_IsValid_RejectsZeroNodeID(t *testing.T) {
	i := &Inbound{
		ID:         uuid.New(),
		NodeID:     uuid.Nil,
		Name:       "x",
		Protocol:   ProtocolVLESS,
		Listen:     "::",
		ListenPort: 443,
	}
	if i.IsValid() {
		t.Fatal("zero node_id should be invalid")
	}
}

func TestInbound_IsValid_RejectsEmptyProtocol(t *testing.T) {
	i := &Inbound{
		ID:         uuid.New(),
		NodeID:     uuid.New(),
		Name:       "x",
		Protocol:   "",
		Listen:     "::",
		ListenPort: 443,
	}
	if i.IsValid() {
		t.Fatal("empty protocol should be invalid")
	}
}

func TestInbound_IsValid_RejectsPortOutOfRange(t *testing.T) {
	for _, port := range []int{0, -1, 65536, 100000} {
		i := &Inbound{
			ID:         uuid.New(),
			NodeID:     uuid.New(),
			Name:       "x",
			Protocol:   ProtocolVLESS,
			Listen:     "::",
			ListenPort: port,
		}
		if i.IsValid() {
			t.Errorf("port %d should be invalid", port)
		}
	}
}

func TestInbound_IsValid_AcceptsPortBoundaries(t *testing.T) {
	for _, port := range []int{1, 80, 443, 8080, 65535} {
		i := &Inbound{
			ID:         uuid.New(),
			NodeID:     uuid.New(),
			Name:       "x",
			Protocol:   ProtocolVLESS,
			Listen:     "::",
			ListenPort: port,
		}
		if !i.IsValid() {
			t.Errorf("port %d should be valid", port)
		}
	}
}

func TestInbound_IsValid_RejectsEmptyListen(t *testing.T) {
	i := &Inbound{
		ID:         uuid.New(),
		NodeID:     uuid.New(),
		Name:       "x",
		Protocol:   ProtocolVLESS,
		Listen:     "",
		ListenPort: 443,
	}
	if i.IsValid() {
		t.Fatal("empty listen should be invalid")
	}
}

func TestValidationError_UnwrapsToErrInvalid(t *testing.T) {
	e := &ValidationError{Field: "name", Message: "must not be empty"}
	if got := e.Unwrap(); !errors.Is(e, ErrInvalid) {
		t.Fatalf("Unwrap = %v, want ErrInvalid (via errors.Is)", got)
	}
}

func TestIsAllowedProtocol(t *testing.T) {
	for _, p := range []Protocol{ProtocolVLESS, ProtocolHysteria2, ProtocolShadowsocks, ProtocolTrojan} {
		if !isAllowedProtocol(p) {
			t.Errorf("%q should be allowed", p)
		}
	}
	for _, p := range []Protocol{"wireguard", "tuic", ""} {
		if isAllowedProtocol(p) {
			t.Errorf("%q should NOT be allowed", p)
		}
	}
}
