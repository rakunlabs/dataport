// Package notebook defines portable SQL notebooks and their dependency graph.
package notebook

import (
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"
)

type Document struct {
	Cells      []Cell      `json:"cells"`
	Parameters []Parameter `json:"parameters"`
}

// Normalize keeps the wire format safe for clients constructing minimal
// documents through the API (Go nil slices must not turn into JSON null).
func (d *Document) Normalize() {
	if d.Cells == nil {
		d.Cells = []Cell{}
	}
	if d.Parameters == nil {
		d.Parameters = []Parameter{}
	}
	for i := range d.Cells {
		c := &d.Cells[i]
		if c.Dependencies == nil {
			c.Dependencies = []string{}
		}
		if c.Transfer != nil && c.Transfer.Mappings == nil {
			c.Transfer.Mappings = []Mapping{}
		}
	}
}

type Cell struct {
	Transfer     *Transfer `json:"transfer,omitempty"`
	ID           string    `json:"id"`
	Path         string    `json:"path"`
	Description  string    `json:"description"`
	ConnectionID string    `json:"connection_id"`
	SQL          string    `json:"sql"`
	Enabled      bool      `json:"enabled"`
	Output       bool      `json:"output"`
	Collapsed    bool      `json:"collapsed"`
	Template     bool      `json:"template"`
	Write        bool      `json:"write"`
	MaxRows      int       `json:"max_rows"`
	TimeoutSec   *int      `json:"timeout_sec,omitempty"`
	Dependencies []string  `json:"dependencies"`
}

type Transfer struct {
	KeepUnmapped bool      `json:"keep_unmapped"`
	ConnectionID string    `json:"connection_id"`
	Schema       string    `json:"schema"`
	Table        string    `json:"table"`
	Wipe         bool      `json:"wipe"`
	Batch        int       `json:"batch"`
	SkipError    string    `json:"skip_error"`
	Mappings     []Mapping `json:"mappings"`
}

type Mapping struct {
	Source   string `json:"source"`
	Target   string `json:"target"`
	Type     string `json:"type"`
	Nullable bool   `json:"nullable"`
	Template string `json:"template"`
	Encoding string `json:"encoding"`
}

type Parameter struct {
	Name     string          `json:"name"`
	Type     string          `json:"type"`
	Required bool            `json:"required"`
	Default  json.RawMessage `json:"default,omitempty"`
}

var PathPattern = regexp.MustCompile(`^[a-zA-Z][a-zA-Z0-9_-]{0,79}$`)

func (d Document) Validate() error {
	if len(d.Cells) > 100 {
		return errors.New("at most 100 cells per notebook")
	}
	ids, paths := map[string]bool{}, map[string]bool{}
	for _, c := range d.Cells {
		if c.Transfer != nil {
			tr := c.Transfer
			if !c.Write || tr.ConnectionID == "" || strings.TrimSpace(tr.Table) == "" || len(tr.Table) > 256 || tr.Batch < 1 || tr.Batch > 500 || len(tr.Mappings) > 500 {
				return fmt.Errorf("invalid transfer settings in %s", c.Path)
			}
			seen := map[string]bool{}
			for _, m := range tr.Mappings {
				if m.Source == "" || m.Target == "" || seen[m.Target] {
					return errors.New("transfer mappings need unique target names and nonempty source names")
				}
				seen[m.Target] = true
				switch m.Type {
				case "", "string", "number", "date":
				default:
					return errors.New("unsupported transfer mapping type")
				}
				if m.Encoding != "" && m.Encoding != "ISO-8859-1" {
					return errors.New("unsupported mapping encoding")
				}
				if len(m.Template) > 4096 {
					return errors.New("mapping template is too long")
				}
			}
		}
		if c.ID == "" || len(c.ID) > 80 || ids[c.ID] {
			return errors.New("cell IDs must be unique and nonempty")
		}
		ids[c.ID] = true
		if !PathPattern.MatchString(c.Path) || paths[c.Path] {
			return errors.New("cell paths must be unique: use a letter followed by letters, numbers, _ or -")
		}
		paths[c.Path] = true
		if c.ConnectionID == "" || strings.TrimSpace(c.SQL) == "" || len(c.SQL) > 1<<20 {
			return fmt.Errorf("cell %s needs a connection and SQL (at most 1 MiB)", c.Path)
		}
		if c.MaxRows < -1 || c.TimeoutSec != nil && (*c.TimeoutSec < 0 || *c.TimeoutSec > 86400) {
			return fmt.Errorf("invalid limits for cell %s", c.Path)
		}
	}
	if len(d.Parameters) > 100 {
		return errors.New("at most 100 parameters")
	}
	names := map[string]bool{}
	for _, p := range d.Parameters {
		if !PathPattern.MatchString(p.Name) || names[p.Name] {
			return errors.New("parameter names must be unique valid paths")
		}
		names[p.Name] = true
		switch p.Type {
		case "string", "number", "boolean", "json":
		default:
			return fmt.Errorf("invalid parameter type for %s", p.Name)
		}
		if len(p.Default) > 0 {
			var v any
			if err := json.Unmarshal(p.Default, &v); err != nil {
				return err
			}
			if !validType(p.Type, v) {
				return fmt.Errorf("invalid default for %s", p.Name)
			}
		}
	}
	_, err := d.Plan("", false)
	return err
}

// Plan is stable in notebook/dependency order. Read-only dependency cells are
// executed once; a disabled dependency is an error, never silently re-enabled.
func (d Document) Plan(target string, enabledOnly bool) ([]Cell, error) {
	byPath := map[string]Cell{}
	for _, c := range d.Cells {
		byPath[c.Path] = c
	}
	state := map[string]int{}
	var out []Cell
	var visit func(Cell) error
	visit = func(c Cell) error {
		if state[c.Path] == 1 {
			return fmt.Errorf("dependency cycle at %s", c.Path)
		}
		if state[c.Path] == 2 {
			return nil
		}
		state[c.Path] = 1
		for _, path := range c.Dependencies {
			dep, ok := byPath[path]
			if !ok {
				return fmt.Errorf("missing dependency %s in %s", path, c.Path)
			}
			if dep.Write {
				return fmt.Errorf("dependency %s must be a read-only SQL cell", path)
			}
			if enabledOnly && !dep.Enabled {
				return fmt.Errorf("dependency %s is disabled", path)
			}
			if err := visit(dep); err != nil {
				return err
			}
		}
		state[c.Path] = 2
		out = append(out, c)
		return nil
	}
	if target != "" {
		c, ok := byPath[target]
		if !ok {
			return nil, errors.New("cell not found")
		}
		if enabledOnly && !c.Enabled {
			return nil, errors.New("cell is disabled")
		}
		if err := visit(c); err != nil {
			return nil, err
		}
	} else {
		for _, c := range d.Cells {
			if !enabledOnly || c.Enabled {
				if err := visit(c); err != nil {
					return nil, err
				}
			}
		}
	}
	return out, nil
}

func validType(t string, v any) bool {
	if v == nil {
		return true
	}
	switch t {
	case "string":
		_, ok := v.(string)
		return ok
	case "number":
		switch v.(type) {
		case float64, json.Number:
			return true
		}
		return false
	case "boolean":
		_, ok := v.(bool)
		return ok
	case "json":
		return true
	}
	return false
}

func (d Document) Inputs(in map[string]any) (map[string]any, error) {
	budget := 10000
	if err := inputBudget(in, 0, &budget); err != nil {
		return nil, err
	}
	out := map[string]any{}
	allowed := map[string]bool{}
	for _, p := range d.Parameters {
		allowed[p.Name] = true
		v, ok := in[p.Name]
		if !ok && len(p.Default) > 0 {
			if err := json.Unmarshal(p.Default, &v); err != nil {
				return nil, err
			}
			ok = true
		}
		if p.Required && (!ok || v == nil) {
			return nil, fmt.Errorf("parameter %s is required", p.Name)
		}
		if !validType(p.Type, v) {
			return nil, fmt.Errorf("parameter %s must be %s", p.Name, p.Type)
		}
		out[p.Name] = v
	}
	for k := range in {
		if !allowed[k] {
			return nil, fmt.Errorf("unknown parameter %s", k)
		}
	}
	return out, nil
}

func inputBudget(v any, depth int, remaining *int) error {
	*remaining--
	if depth > 20 || *remaining < 0 {
		return errors.New("notebook input is too large or deeply nested")
	}
	switch v := v.(type) {
	case map[string]any:
		for _, child := range v {
			if err := inputBudget(child, depth+1, remaining); err != nil {
				return err
			}
		}
	case []any:
		for _, child := range v {
			if err := inputBudget(child, depth+1, remaining); err != nil {
				return err
			}
		}
	}
	return nil
}
