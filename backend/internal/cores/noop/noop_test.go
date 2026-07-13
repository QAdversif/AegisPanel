// SPDX-License-Identifier: AGPL-3.0-or-later

package noop

import (
	"context"
	"strings"
	"testing"

	"github.com/QAdversif/AegisPanel/internal/cores"
)

func TestNoop_NameAndVersion(t *testing.T) {
	p := New("alt", "1.2.3")
	if p.Name() != "alt" {
		t.Fatalf("Name = %q, want alt", p.Name())
	}
	if p.Version() != "1.2.3" {
		t.Fatalf("Version = %q, want 1.2.3", p.Version())
	}
}

func TestNoop_DefaultsWhenEmptyArgs(t *testing.T) {
	// New("") and New(x, "") should not blow up — they
	// fall back to sane defaults so the dev panel does not
	// have to special-case empty config values.
	p := New("", "")
	if p.Name() != "noop" {
		t.Fatalf("Name = %q, want noop", p.Name())
	}
	if p.Version() == "" {
		t.Fatal("Version should default to non-empty")
	}
}

func TestNoop_CapabilitiesHaveNoFlags(t *testing.T) {
	// The noop provider advertises nothing — that is the
	// whole point. The panel UI relies on the empty
	// capability set to render "noop provider" as a
	// distinct row.
	p := New("test", "1.0")
	caps := p.Capabilities()
	if caps.Name != "test" {
		t.Fatalf("Capabilities.Name = %q, want test", caps.Name)
	}
	if len(caps.Flags) != 0 {
		t.Fatalf("Flags should be empty, got %v", caps.Flags)
	}
}

func TestNoop_RenderConfig_IsValidJSON(t *testing.T) {
	p := New("test", "1.0")
	out, err := p.RenderConfig(context.Background(), cores.CoreConfig{})
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	// Noop stamps its own name into Experimental so the
	// rendered output is recognisable in logs and tests.
	if !strings.Contains(out, "noop_provider") {
		t.Fatalf("rendered config missing noop_provider marker: %s", out)
	}
	// Round-trip through ValidateConfig — anything we
	// produce, we must also accept.
	if err := p.ValidateConfig(context.Background(), []byte(out)); err != nil {
		t.Fatalf("round-trip validate: %v", err)
	}
}

func TestNoop_ValidateConfig_RejectsEmpty(t *testing.T) {
	p := New("test", "1.0")
	if err := p.ValidateConfig(context.Background(), nil); err == nil {
		t.Fatal("expected error for empty config, got nil")
	}
}

func TestNoop_ValidateConfig_RejectsInvalidJSON(t *testing.T) {
	p := New("test", "1.0")
	if err := p.ValidateConfig(context.Background(), []byte("not json")); err == nil {
		t.Fatal("expected error for non-JSON, got nil")
	}
}

func TestNoop_Diff_EmptyWhenEqual(t *testing.T) {
	p := New("test", "1.0")
	raw := []byte("{}")
	d, err := p.Diff(raw, raw)
	if err != nil {
		t.Fatalf("diff: %v", err)
	}
	if d != "" {
		t.Fatalf("expected empty diff for equal configs, got %q", d)
	}
}

func TestNoop_Diff_NonEmptyWhenDifferent(t *testing.T) {
	p := New("test", "1.0")
	d, err := p.Diff([]byte("a"), []byte("b"))
	if err != nil {
		t.Fatalf("diff: %v", err)
	}
	if d == "" {
		t.Fatal("expected non-empty diff for different configs")
	}
}

func TestNoop_Apply_AlwaysSucceeds(t *testing.T) {
	p := New("test", "1.0")
	if err := p.Apply(context.Background(), "any-node", []byte("{}")); err != nil {
		t.Fatalf("apply: %v", err)
	}
}

func TestNoop_ParseStatus(t *testing.T) {
	p := New("test", "1.0")
	s, err := p.ParseStatus(nil)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if s.Status != "noop" {
		t.Fatalf("status = %q, want noop", s.Status)
	}
	if s.Version != "1.0" {
		t.Fatalf("version = %q, want 1.0", s.Version)
	}
}

func TestNoop_ParseStats_Empty(t *testing.T) {
	p := New("test", "1.0")
	stats, err := p.ParseStats([]byte("anything"))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(stats) != 0 {
		t.Fatalf("stats should be empty, got %v", stats)
	}
}
