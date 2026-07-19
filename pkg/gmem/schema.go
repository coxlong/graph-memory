package gmem

import (
	"fmt"
	"regexp"
	"slices"
	"strings"
)

type AttributeDef struct {
	Type     string `yaml:"type" json:"type"`
	Required bool   `yaml:"required" json:"required,omitempty"`
}

type EntityTypeDef struct {
	Description string                  `yaml:"description" json:"description,omitempty"`
	Attributes  map[string]AttributeDef `yaml:"attributes" json:"attributes,omitempty"`
}

type EdgeTypeDef struct {
	Description string   `yaml:"description" json:"description,omitempty"`
	Source      []string `yaml:"source" json:"source,omitempty"`
	Target      []string `yaml:"target" json:"target,omitempty"`
}

type Schema struct {
	EntityTypes map[string]EntityTypeDef `yaml:"entity_types" json:"entity_types,omitempty"`
	EdgeTypes   map[string]EdgeTypeDef   `yaml:"edge_types" json:"edge_types,omitempty"`
}

var labelRe = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

// ValidateLabels checks that labels are safe for Cypher interpolation
func ValidateLabels(labels []string) error {
	for _, l := range labels {
		if !labelRe.MatchString(l) {
			return fmt.Errorf("invalid label %q", l)
		}
	}
	return nil
}

// PrimaryType returns the first non-Entity label that has a config entry
func (s *Schema) PrimaryType(labels []string) string {
	for _, l := range labels {
		if l == "Entity" {
			continue
		}
		if _, ok := s.EntityTypes[l]; ok {
			return l
		}
	}
	return ""
}

func (s *Schema) ValidateEntity(labels []string, attrs map[string]any, lenient bool) error {
	if lenient || len(s.EntityTypes) == 0 {
		return nil
	}
	t := s.PrimaryType(labels)
	if t == "" {
		return fmt.Errorf("no configured entity type in labels %v", labels)
	}
	def := s.EntityTypes[t]
	for name, ad := range def.Attributes {
		v, ok := attrs[name]
		if !ok || v == nil {
			if ad.Required {
				return fmt.Errorf("missing required attribute %q for type %s", name, t)
			}
			continue
		}
		if err := checkAttrType(name, ad.Type, v); err != nil {
			return err
		}
	}
	for name := range attrs {
		if _, ok := def.Attributes[name]; !ok {
			return fmt.Errorf("undefined attribute %q for type %s (use --lenient to skip)", name, t)
		}
	}
	return nil
}

func checkAttrType(name, typ string, v any) error {
	switch {
	case typ == "string":
		if _, ok := v.(string); !ok {
			return fmt.Errorf("attribute %q must be string", name)
		}
	case strings.HasPrefix(typ, "enum:"):
		sv, ok := v.(string)
		if !ok {
			return fmt.Errorf("attribute %q must be string (enum)", name)
		}
		allowed := strings.Split(strings.TrimPrefix(typ, "enum:"), "|")
		if !slices.Contains(allowed, sv) {
			return fmt.Errorf("attribute %q: %q not in enum [%s]", name, sv, strings.Join(allowed, " "))
		}
	case typ == "number":
		switch v.(type) {
		case int, int64, float64:
		default:
			return fmt.Errorf("attribute %q must be number", name)
		}
	}
	return nil
}

func (s *Schema) ValidateEdge(name string, sourceLabels, targetLabels []string, lenient bool) error {
	if lenient || len(s.EdgeTypes) == 0 {
		return nil
	}
	def, ok := s.EdgeTypes[name]
	if !ok {
		return fmt.Errorf("undefined edge type %q (use --lenient to skip)", name)
	}
	if !hasAny(sourceLabels, def.Source) {
		return fmt.Errorf("edge %s: source labels %v not in %v", name, sourceLabels, def.Source)
	}
	if !hasAny(targetLabels, def.Target) {
		return fmt.Errorf("edge %s: target labels %v not in %v", name, targetLabels, def.Target)
	}
	return nil
}

func hasAny(labels, allowed []string) bool {
	for _, l := range labels {
		if l == "Entity" {
			continue
		}
		if slices.Contains(allowed, l) {
			return true
		}
	}
	return false
}
