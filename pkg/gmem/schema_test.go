package gmem

import (
	"os"
	"path/filepath"
	"testing"
)

const testSchemaYAML = `
entity_types:
  Person:
    description: "真实人物"
    attributes:
      role: { type: string, required: true }
      team: { type: string }
  Project:
    attributes:
      status: { type: "enum:active|paused|done", required: true }
edge_types:
  WORKS_ON:
    source: [Person]
    target: [Project]
`

func loadTestSchema(t *testing.T) *Schema {
	t.Helper()
	p := filepath.Join(t.TempDir(), "gmem.yaml")
	if err := os.WriteFile(p, []byte(testSchemaYAML), 0o644); err != nil {
		t.Fatal(err)
	}
	s, err := LoadSchema(p)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func TestValidateEntityOK(t *testing.T) {
	s := loadTestSchema(t)
	err := s.ValidateEntity([]string{"Person"}, map[string]any{"role": "backend"}, false)
	if err != nil {
		t.Fatal(err)
	}
}

func TestValidateEntityMissingRequired(t *testing.T) {
	s := loadTestSchema(t)
	err := s.ValidateEntity([]string{"Person"}, map[string]any{"team": "B"}, false)
	if err == nil {
		t.Fatal("expected missing required error")
	}
}

func TestValidateEntityEnum(t *testing.T) {
	s := loadTestSchema(t)
	if err := s.ValidateEntity([]string{"Project"}, map[string]any{"status": "bogus"}, false); err == nil {
		t.Fatal("expected enum error")
	}
	if err := s.ValidateEntity([]string{"Project"}, map[string]any{"status": "active"}, false); err != nil {
		t.Fatal(err)
	}
}

func TestValidateEntityUndefinedAttr(t *testing.T) {
	s := loadTestSchema(t)
	if err := s.ValidateEntity([]string{"Person"}, map[string]any{"role": "x", "zzz": 1}, false); err == nil {
		t.Fatal("expected undefined attribute error")
	}
	if err := s.ValidateEntity([]string{"Person"}, map[string]any{"role": "x", "zzz": 1}, true); err != nil {
		t.Fatal("lenient should pass")
	}
}

func TestValidateEdgeEndpoints(t *testing.T) {
	s := loadTestSchema(t)
	if err := s.ValidateEdge("WORKS_ON", []string{"Entity", "Person"}, []string{"Entity", "Project"}, false); err != nil {
		t.Fatal(err)
	}
	if err := s.ValidateEdge("WORKS_ON", []string{"Entity", "Project"}, []string{"Entity", "Person"}, false); err == nil {
		t.Fatal("expected endpoint type error")
	}
	if err := s.ValidateEdge("UNKNOWN_EDGE", []string{"Person"}, []string{"Project"}, false); err == nil {
		t.Fatal("expected undefined edge type error")
	}
}

func TestValidateLabelsInjection(t *testing.T) {
	if err := ValidateLabels([]string{"Person"}); err != nil {
		t.Fatal(err)
	}
	if err := ValidateLabels([]string{"Person} DETACH DELETE n //"}); err == nil {
		t.Fatal("expected injection rejection")
	}
}

func TestPrimaryType(t *testing.T) {
	s := loadTestSchema(t)
	if got := s.PrimaryType([]string{"Entity", "Person", "Manager"}); got != "Person" {
		t.Fatalf("primary type: %q", got)
	}
}
