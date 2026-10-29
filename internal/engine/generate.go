package engine

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"math"
	"math/rand"
	"slices"
	"strconv"
	"strings"
	"text/template"
	"time"
	"unicode/utf8"

	"github.com/jaswdr/faker"
	"github.com/spf13/cast"

	"github.com/rakunlabs/dataport/internal/notebook"
	"github.com/rakunlabs/dataport/internal/store"
)

// Generating test data: rows of made-up values, chosen per column from the
// column's name and type, inserted in batches as an import is.

// GenColumn says how the values of one column are made.
type GenColumn struct {
	Name string `json:"name"`
	// Type, Nullable and Auto describe the column; the server fills them.
	Type     string `json:"type,omitempty"`
	Nullable bool   `json:"nullable,omitempty"`
	// Auto is "identity" for columns the database numbers and "generated"
	// for those it computes, which cannot be written at all.
	Auto      string `json:"auto,omitempty"`
	Generator string `json:"generator"`
	// Min and Max bound numbers, sequences (Min is the start) and the
	// length of text.
	Min *float64 `json:"min,omitempty"`
	Max *float64 `json:"max,omitempty"`
	// Decimals of a decimal number.
	Decimals *int `json:"decimals,omitempty"`
	// From and To bound dates, as 2006-01-02 or RFC 3339.
	From string `json:"from,omitempty"`
	To   string `json:"to,omitempty"`
	// Values to pick from for list, the value of constant.
	Values []string `json:"values,omitempty"`
	// Template is a mugo template for template; .row holds the values made
	// so far for the row, .n its number.
	Template string `json:"template,omitempty"`
	// Ref is the column fk takes existing values from.
	Ref *GenRef `json:"ref,omitempty"`
	// NullPercent of the rows get NULL, for nullable columns.
	NullPercent float64 `json:"null_percent,omitempty"`
}

type GenRef struct {
	Schema string `json:"schema"`
	Table  string `json:"table"`
	Column string `json:"column"`
}

type GenerateRequest struct {
	Schema  string
	Table   string
	Columns []GenColumn
	Rows    int
	// Seed makes the same values again; 0 picks one.
	Seed     int64
	Truncate bool
	Timeout  time.Duration
	// Pools holds values made elsewhere, such as in another language, for
	// some generators; those generators pick from them instead.
	Pools map[string][]string
}

// Localizable names the generators Pools may replace.
var Localizable = []string{
	"first_name", "last_name", "full_name", "gender", "phone", "company", "job_title",
	"street", "city", "state", "country", "postcode", "address", "word", "sentence", "paragraph",
}

const maxPoolValues = 5000

// GeneratePlan is what a preview returns: every column of the table with
// the generator it gets, and a few rows.
type GeneratePlan struct {
	Columns []GenColumn `json:"columns"`
	Rows    [][]any     `json:"rows"`
	Seed    int64       `json:"seed"`
	// Generators names every generator a column may use.
	Generators []string `json:"generators"`
}

type GenerateProgress struct {
	Rows int64 `json:"rows"`
	Done bool  `json:"done,omitempty"`
}

var ErrBadGenerate = errors.New("invalid data generation")

const (
	MaxGenerateRows = 1_000_000
	previewRows     = 20
	fkSampleRows    = 10_000
)

// Generators lists the generator names, for clients and the fake function
// of templates; the special ones (skip, null, list, template…) cannot be
// called from a template.
func Generators() []string {
	out := make([]string, 0, len(generatorOrder)+len(specialGenerators))
	out = append(out, specialGenerators...)
	out = append(out, generatorOrder...)

	return out
}

var specialGenerators = []string{"skip", "null", "constant", "list", "template", "fk", "sequence"}

// ////////////////////////////////////////////
// running

// GeneratePreview makes a few rows without writing anything, with the
// generators the request names and guesses for the other columns.
func (m *Manager) GeneratePreview(ctx context.Context, c *store.Connection, password string, req GenerateRequest) (*GeneratePlan, error) {
	if strings.TrimSpace(req.Table) == "" {
		return nil, fmt.Errorf("%w: table is required", ErrBadGenerate)
	}

	out := &GeneratePlan{Generators: Generators()}

	err := m.Session(ctx, c, password, req.Timeout, func(ctx context.Context, d Driver, conn *sql.Conn) error {
		g, err := newGenerator(ctx, d, conn, req)
		if err != nil {
			return err
		}

		out.Columns, out.Seed = g.cols, g.seed

		for i := range min(max(req.Rows, 1), previewRows) {
			row, err := g.row(i)
			if err != nil {
				return err
			}

			for j, v := range row {
				row[j] = jsonValue(v)
			}

			out.Rows = append(out.Rows, row)
		}

		return nil
	})
	if err != nil {
		return nil, err
	}

	return out, nil
}

// Generate inserts the rows in one transaction where the database has
// them, reporting progress as they go in.
func (m *Manager) Generate(ctx context.Context, c *store.Connection, password string, req GenerateRequest, progress func(GenerateProgress)) (int64, error) {
	switch {
	case strings.TrimSpace(req.Table) == "":
		return 0, fmt.Errorf("%w: table is required", ErrBadGenerate)
	case req.Rows < 1 || req.Rows > MaxGenerateRows:
		return 0, fmt.Errorf("%w: rows must be between 1 and %d", ErrBadGenerate, MaxGenerateRows)
	}

	var inserted int64

	err := m.Session(ctx, c, password, req.Timeout, func(ctx context.Context, d Driver, conn *sql.Conn) error {
		return inTransaction(ctx, d, conn, req.Timeout, func(run Runner) error {
			if req.Truncate {
				if _, err := run.ExecContext(ctx, "DELETE FROM "+qualify(d, req.Schema, req.Table)); err != nil {
					return fmt.Errorf("empty table: %s", cleanError(ctx, err))
				}
			}

			g, err := newGenerator(ctx, d, run, req)
			if err != nil {
				return err
			}

			ins := NewInserter(ctx, d, run, req.Schema, req.Table, g.names)
			last := time.Now()

			for i := range req.Rows {
				row, err := g.row(i)
				if err != nil {
					return fmt.Errorf("row %d: %w", i+1, err)
				}

				if err := ins.Add(row); err != nil {
					return fmt.Errorf("row %d: %w", i+1, err)
				}

				if time.Since(last) > 500*time.Millisecond {
					last = time.Now()
					progress(GenerateProgress{Rows: ins.Inserted})
				}
			}

			if err := ins.Flush(); err != nil {
				return err
			}

			inserted = ins.Inserted

			return nil
		})
	})

	return inserted, err
}

// DescribeColumns sums up the generators of the columns, for the query log.
func DescribeColumns(cols []GenColumn) string {
	parts := make([]string, 0, len(cols))

	for _, c := range cols {
		if c.Generator == "skip" {
			continue
		}

		s := c.Name + "=" + c.Generator
		if c.Generator == "template" {
			s += "(" + c.Template + ")"
		} else if c.Generator == "fk" && c.Ref != nil {
			s += "(" + c.Ref.Table + "." + c.Ref.Column + ")"
		}

		parts = append(parts, s)
	}

	return strings.Join(parts, ", ")
}

// ////////////////////////////////////////////
// generator

type generator struct {
	cols  []GenColumn
	names []string
	seed  int64
	rng   *rand.Rand
	fake  faker.Faker
	pools map[string][]string
	// per inserted column, in names order
	plan []colPlan
	// order the columns are made in: templates last, so they see the rest
	order []int
}

type colPlan struct {
	col     GenColumn
	family  string
	length  int
	make    func(n int, row map[string]any) (any, error)
	convert func(any) any
}

func newGenerator(ctx context.Context, d Driver, q Runner, req GenerateRequest) (*generator, error) {
	tableCols, err := d.Columns(ctx, q, req.Schema, req.Table)
	if err != nil {
		return nil, err
	}

	if len(tableCols) == 0 {
		return nil, fmt.Errorf("%w: table %q not found", ErrBadGenerate, req.Table)
	}

	var fks []ForeignKey
	if s, ok := d.(Structurer); ok {
		// Without foreign keys the guesses are only less useful.
		fks, _ = s.ForeignKeys(ctx, q, req.Schema, req.Table)
	}

	auto := autoColumns(ctx, d, q, req.Schema, req.Table)

	asked := make(map[string]GenColumn, len(req.Columns))
	for _, c := range req.Columns {
		asked[strings.ToLower(c.Name)] = c
	}

	for name := range asked {
		found := false

		for _, tc := range tableCols {
			if strings.EqualFold(tc.Name, name) {
				found = true

				break
			}
		}

		if !found {
			return nil, fmt.Errorf("%w: the table has no column %q", ErrBadGenerate, name)
		}
	}

	seed := req.Seed
	if seed == 0 {
		// Small enough to stay exact in a JavaScript number.
		seed = time.Now().UnixNano()%1_000_000_000 + 1
	}

	for name, values := range req.Pools {
		switch {
		case !slices.Contains(Localizable, name):
			return nil, fmt.Errorf("%w: generator %q takes no values", ErrBadGenerate, name)
		case len(values) > maxPoolValues:
			return nil, fmt.Errorf("%w: at most %d values for %q", ErrBadGenerate, maxPoolValues, name)
		}
	}

	src := rand.NewSource(seed)
	g := &generator{seed: seed, rng: rand.New(src), fake: faker.NewWithSeed(src), pools: req.Pools}

	single := 0
	for _, tc := range tableCols {
		if tc.PrimaryKey {
			single++
		}
	}

	for _, tc := range tableCols {
		guess := GuessColumn(d.Type(), tc, auto[strings.ToLower(tc.Name)], fkFor(fks, tc.Name, req.Schema), single == 1)

		col := guess
		if a, ok := asked[strings.ToLower(tc.Name)]; ok {
			col = a
			col.Name, col.Type, col.Nullable, col.Auto = tc.Name, tc.Type, tc.Nullable, guess.Auto

			if col.Generator == "" {
				col.Generator = guess.Generator
			}

			if col.Generator == "fk" && col.Ref == nil {
				col.Ref = guess.Ref
			}
		}

		if col.Auto == "generated" && col.Generator != "skip" {
			return nil, fmt.Errorf("%w: column %q is filled by the database and cannot be written", ErrBadGenerate, tc.Name)
		}

		if !col.Nullable {
			if col.Generator == "null" {
				return nil, fmt.Errorf("%w: column %q does not allow NULL", ErrBadGenerate, tc.Name)
			}

			col.NullPercent = 0
		}

		col.NullPercent = min(max(col.NullPercent, 0), 100)
		g.cols = append(g.cols, col)

		if col.Generator == "skip" {
			continue
		}

		p, err := g.compile(ctx, d, q, req, col)
		if err != nil {
			return nil, fmt.Errorf("%w: column %q: %w", ErrBadGenerate, tc.Name, err)
		}

		g.names = append(g.names, tc.Name)
		g.plan = append(g.plan, p)
	}

	if len(g.plan) == 0 {
		return nil, fmt.Errorf("%w: every column is skipped", ErrBadGenerate)
	}

	for i, p := range g.plan {
		if p.col.Generator != "template" {
			g.order = append(g.order, i)
		}
	}

	for i, p := range g.plan {
		if p.col.Generator == "template" {
			g.order = append(g.order, i)
		}
	}

	return g, nil
}

func (g *generator) row(n int) ([]any, error) {
	out := make([]any, len(g.plan))
	values := make(map[string]any, len(g.plan))

	for _, i := range g.order {
		p := g.plan[i]

		var v any

		if p.col.NullPercent > 0 && g.rng.Float64()*100 < p.col.NullPercent {
			v = nil
		} else {
			var err error
			if v, err = p.make(n, values); err != nil {
				return nil, fmt.Errorf("%s: %w", p.col.Name, err)
			}
		}

		values[p.col.Name] = v
		out[i] = p.convert(v)
	}

	return out, nil
}

func (g *generator) compile(ctx context.Context, d Driver, q Runner, req GenerateRequest, col GenColumn) (colPlan, error) {
	family, length := columnShape(col.Type)
	p := colPlan{col: col, family: family, length: length}
	p.convert = fitValue(d.Type(), col.Type, family, length)

	switch col.Generator {
	case "null":
		p.make = func(int, map[string]any) (any, error) { return nil, nil }
	case "constant":
		v := ""
		if len(col.Values) > 0 {
			v = col.Values[0]
		}

		p.make = func(int, map[string]any) (any, error) { return v, nil }
	case "list":
		if len(col.Values) == 0 {
			return p, errors.New("list needs values")
		}

		values := col.Values
		p.make = func(int, map[string]any) (any, error) { return values[g.rng.Intn(len(values))], nil }
	case "sequence":
		start := int64(1)

		if col.Min != nil {
			start = int64(*col.Min)
		} else if top, err := maxOf(ctx, d, q, req.Schema, req.Table, col.Name); err == nil {
			start = top + 1
		}

		p.make = func(n int, _ map[string]any) (any, error) { return start + int64(n), nil }
	case "fk":
		if col.Ref == nil || col.Ref.Table == "" || col.Ref.Column == "" {
			return p, errors.New("fk needs the referenced table and column")
		}

		values, err := sampleColumn(ctx, d, q, col.Ref.Schema, col.Ref.Table, col.Ref.Column)
		if err != nil {
			return p, fmt.Errorf("read %s.%s: %s", col.Ref.Table, col.Ref.Column, cleanError(ctx, err))
		}

		if len(values) == 0 {
			if !col.Nullable {
				return p, fmt.Errorf("%s has no rows to refer to", col.Ref.Table)
			}

			p.make = func(int, map[string]any) (any, error) { return nil, nil }

			break
		}

		p.make = func(int, map[string]any) (any, error) { return values[g.rng.Intn(len(values))], nil }
	case "template":
		t, err := g.template(col.Template)
		if err != nil {
			return p, err
		}

		p.make = func(n int, row map[string]any) (any, error) {
			var b mappingBuffer
			if err := t.Execute(&b, map[string]any{"row": row, "n": n + 1}); err != nil {
				return nil, err
			}

			return b.String(), nil
		}
	default:
		if _, ok := generators[col.Generator]; !ok {
			return p, fmt.Errorf("unknown generator %q", col.Generator)
		}

		opts := genOptions{col: col, family: family, length: length}
		if err := opts.parseDates(); err != nil {
			return p, err
		}

		name := col.Generator
		p.make = func(int, map[string]any) (any, error) { return g.value(name, opts), nil }
	}

	return p, nil
}

// value makes a value of a known generator, from its pool when it has one.
func (g *generator) value(name string, o genOptions) any {
	if name == "words" {
		if pool := g.pools["word"]; len(pool) > 0 {
			lo, hi := o.bounds(2, 5)
			words := make([]string, int(lo)+g.rng.Intn(int(hi-lo)+1))

			for i := range words {
				words[i] = pool[g.rng.Intn(len(pool))]
			}

			return strings.Join(words, " ")
		}
	}

	if pool := g.pools[name]; len(pool) > 0 {
		return pool[g.rng.Intn(len(pool))]
	}

	return generators[name](g, o)
}

// template parses a value template with the generator's functions: fake
// "email" makes a value as that generator does, randInt and oneOf pick.
func (g *generator) template(text string) (*template.Template, error) {
	if strings.TrimSpace(text) == "" {
		return nil, errors.New("the template is empty")
	}

	funcs := notebook.TemplateFuncs()
	funcs["fake"] = func(name string) (any, error) {
		if _, ok := generators[name]; !ok {
			return nil, fmt.Errorf("fake: unknown generator %q", name)
		}

		return g.value(name, genOptions{family: "text"}), nil
	}
	funcs["randInt"] = func(lo, hi any) (int64, error) {
		a, b := cast.ToInt64(lo), cast.ToInt64(hi)
		if b < a {
			return 0, errors.New("randInt: max is below min")
		}

		return a + g.rng.Int63n(b-a+1), nil
	}
	funcs["oneOf"] = func(values ...any) (any, error) {
		if len(values) == 0 {
			return nil, errors.New("oneOf: no values")
		}

		return values[g.rng.Intn(len(values))], nil
	}

	t, err := template.New("value").Option("missingkey=zero").Funcs(funcs).Parse(text)
	if err != nil {
		return nil, err
	}

	if len(t.Templates()) != 1 {
		return nil, errors.New("templates cannot define templates")
	}

	if err := notebook.ValidateValueTemplate(t.Tree.Root, "fake", "randInt", "oneOf"); err != nil { //nolint:staticcheck
		return nil, err
	}

	return t, nil
}

// ////////////////////////////////////////////
// values

type genOptions struct {
	col      GenColumn
	family   string
	length   int
	from, to time.Time
}

func (o *genOptions) parseDates() error {
	// Whole days, so a preview and the run after it make the same dates.
	o.to = time.Now().UTC().Truncate(24 * time.Hour)
	o.from = o.to.AddDate(-2, 0, 0)

	for _, p := range []struct {
		s   string
		dst *time.Time
	}{{o.col.From, &o.from}, {o.col.To, &o.to}} {
		if strings.TrimSpace(p.s) == "" {
			continue
		}

		t, err := parseDate(p.s)
		if err != nil {
			return err
		}

		*p.dst = t
	}

	if o.to.Before(o.from) {
		return errors.New("the date range ends before it starts")
	}

	return nil
}

func parseDate(s string) (time.Time, error) {
	s = strings.TrimSpace(s)
	for _, layout := range []string{time.RFC3339, "2006-01-02 15:04:05", "2006-01-02T15:04:05", "2006-01-02"} {
		if t, err := time.Parse(layout, s); err == nil {
			return t, nil
		}
	}

	return time.Time{}, fmt.Errorf("invalid date %q, use 2006-01-02", s)
}

// bounds returns Min and Max, or the defaults.
func (o genOptions) bounds(lo, hi float64) (float64, float64) {
	if o.col.Min != nil {
		lo = *o.col.Min
	}

	if o.col.Max != nil {
		hi = *o.col.Max
	}

	if hi < lo {
		hi = lo
	}

	return lo, hi
}

type genFunc func(g *generator, o genOptions) any

var generatorOrder = []string{
	"first_name", "last_name", "full_name", "username", "email", "password", "phone", "gender",
	"company", "job_title", "street", "city", "state", "country", "country_code", "postcode", "address",
	"latitude", "longitude", "url", "domain", "ipv4", "ipv6", "mac", "user_agent", "slug",
	"color", "currency_code", "language", "credit_card", "code",
	"word", "words", "sentence", "paragraph", "uuid", "int", "decimal", "bool",
	"date", "timestamp", "time", "json", "bytes",
}

var generators = map[string]genFunc{
	"first_name": func(g *generator, _ genOptions) any { return g.fake.Person().FirstName() },
	"last_name":  func(g *generator, _ genOptions) any { return g.fake.Person().LastName() },
	"full_name": func(g *generator, _ genOptions) any {
		p := g.fake.Person()
		return p.FirstName() + " " + p.LastName()
	},
	"username": func(g *generator, _ genOptions) any {
		return strings.ToLower(g.fake.Person().FirstName()) + strconv.Itoa(g.rng.Intn(1000))
	},
	"email": func(g *generator, _ genOptions) any {
		p := g.fake.Person()
		return strings.ToLower(asciiOnly(p.FirstName())+"."+asciiOnly(p.LastName())) + strconv.Itoa(g.rng.Intn(100)) + "@" + g.fake.Internet().FreeEmailDomain()
	},
	"password":      func(g *generator, _ genOptions) any { return g.fake.Internet().Password() },
	"phone":         func(g *generator, _ genOptions) any { return g.fake.Phone().E164Number() },
	"gender":        func(g *generator, _ genOptions) any { return g.fake.Gender().Name() },
	"company":       func(g *generator, _ genOptions) any { return g.fake.Company().Name() },
	"job_title":     func(g *generator, _ genOptions) any { return g.fake.Company().JobTitle() },
	"street":        func(g *generator, _ genOptions) any { return g.fake.Address().StreetAddress() },
	"city":          func(g *generator, _ genOptions) any { return g.fake.Address().City() },
	"state":         func(g *generator, _ genOptions) any { return g.fake.Address().State() },
	"country":       func(g *generator, _ genOptions) any { return g.fake.Address().Country() },
	"country_code":  func(g *generator, _ genOptions) any { return g.fake.Address().CountryCode() },
	"postcode":      func(g *generator, _ genOptions) any { return g.fake.Address().PostCode() },
	"address":       func(g *generator, _ genOptions) any { return g.fake.Address().Address() },
	"latitude":      func(g *generator, _ genOptions) any { return round(g.rng.Float64()*180-90, 6) },
	"longitude":     func(g *generator, _ genOptions) any { return round(g.rng.Float64()*360-180, 6) },
	"url":           func(g *generator, _ genOptions) any { return g.fake.Internet().URL() },
	"domain":        func(g *generator, _ genOptions) any { return g.fake.Internet().Domain() },
	"ipv4":          func(g *generator, _ genOptions) any { return g.fake.Internet().Ipv4() },
	"ipv6":          func(g *generator, _ genOptions) any { return g.fake.Internet().Ipv6() },
	"mac":           func(g *generator, _ genOptions) any { return g.fake.Internet().MacAddress() },
	"user_agent":    func(g *generator, _ genOptions) any { return g.fake.UserAgent().UserAgent() },
	"slug":          func(g *generator, _ genOptions) any { return g.fake.Internet().Slug() },
	"color":         func(g *generator, _ genOptions) any { return g.fake.Color().Hex() },
	"currency_code": func(g *generator, _ genOptions) any { return g.fake.Currency().Code() },
	"language":      func(g *generator, _ genOptions) any { return g.fake.Language().LanguageAbbr() },
	"credit_card":   func(g *generator, _ genOptions) any { return g.fake.Payment().CreditCardNumber() },
	"code":          func(g *generator, _ genOptions) any { return strings.ToUpper(g.fake.Bothify("??-####")) },
	"word":          func(g *generator, _ genOptions) any { return g.fake.Lorem().Word() },
	"words": func(g *generator, o genOptions) any {
		lo, hi := o.bounds(2, 5)
		return strings.Join(g.fake.Lorem().Words(int(lo)+g.rng.Intn(int(hi-lo)+1)), " ")
	},
	"sentence": func(g *generator, _ genOptions) any { return g.fake.Lorem().Sentence(4 + g.rng.Intn(8)) },
	"paragraph": func(g *generator, _ genOptions) any {
		return g.fake.Lorem().Paragraph(2 + g.rng.Intn(4))
	},
	"uuid": func(g *generator, _ genOptions) any {
		var b [16]byte

		_, _ = g.rng.Read(b[:])
		b[6] = (b[6] & 0x0f) | 0x40
		b[8] = (b[8] & 0x3f) | 0x80

		return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:])
	},
	"int": func(g *generator, o genOptions) any {
		dlo, dhi := intRange(o.family)
		lo, hi := o.bounds(dlo, dhi)
		a, b := int64(math.Ceil(lo)), int64(math.Floor(hi))

		if b <= a {
			return a
		}

		return a + g.rng.Int63n(b-a+1)
	},
	"decimal": func(g *generator, o genOptions) any {
		lo, hi := o.bounds(0, 1000)
		dec := 2

		if o.col.Decimals != nil {
			dec = min(max(*o.col.Decimals, 0), 10)
		}

		return round(lo+g.rng.Float64()*(hi-lo), dec)
	},
	"bool": func(g *generator, _ genOptions) any { return g.rng.Intn(2) == 1 },
	"date": func(g *generator, o genOptions) any {
		t := randomTime(g, o)
		return time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, time.UTC)
	},
	"timestamp": func(g *generator, o genOptions) any { return randomTime(g, o) },
	"time": func(g *generator, _ genOptions) any {
		s := g.rng.Intn(86400)
		return fmt.Sprintf("%02d:%02d:%02d", s/3600, s/60%60, s%60)
	},
	"json": func(g *generator, _ genOptions) any {
		return fmt.Sprintf(`{"id": %d, "name": %q, "active": %t}`, g.rng.Intn(10000), g.fake.Lorem().Word(), g.rng.Intn(2) == 1)
	},
	"bytes": func(g *generator, o genOptions) any {
		lo, hi := o.bounds(16, 16)
		b := make([]byte, int(lo)+g.rng.Intn(int(hi-lo)+1))
		_, _ = g.rng.Read(b)

		return b
	},
}

func randomTime(g *generator, o genOptions) time.Time {
	span := o.to.Unix() - o.from.Unix()
	if span <= 0 {
		return o.from
	}

	return time.Unix(o.from.Unix()+g.rng.Int63n(span+1), 0).UTC()
}

func intRange(family string) (float64, float64) {
	switch family {
	case "smallint":
		return 1, 1000
	case "int":
		return 1, 100_000
	}

	return 1, 1_000_000
}

func round(f float64, dec int) float64 {
	p := math.Pow(10, float64(dec))
	return math.Round(f*p) / p
}

func asciiOnly(s string) string {
	var b strings.Builder

	for _, r := range s {
		if r < utf8.RuneSelf && (r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9') {
			b.WriteRune(r)
		}
	}

	return b.String()
}

// columnShape returns the type family of a column and its length for
// character types, 0 when unbounded.
func columnShape(typ string) (string, int) {
	t := strings.ToLower(strings.TrimSpace(typ))

	for _, w := range []string{"nullable(", "lowcardinality("} {
		for strings.HasPrefix(t, w) && strings.HasSuffix(t, ")") {
			t = strings.TrimSpace(t[len(w) : len(t)-1])
		}
	}

	family := typeFamily(strings.TrimSpace(typeArgs.ReplaceAllString(t, "")))

	switch {
	case t == "string" || strings.HasPrefix(t, "fixedstring"):
		if family != "text" {
			family = "text"
		}
	case family == "text" && (strings.HasPrefix(t, "int") || strings.HasPrefix(t, "uint")):
		family = "bigint"
	case strings.HasPrefix(t, "float"):
		family = "double"
	case t == "integer":
		family = "int"
	}

	length := 0

	if family == "varchar" || family == "char" || family == "text" {
		if m := typeArgs.FindStringSubmatch(t); m != nil {
			n, _ := strconv.Atoi(strings.TrimSpace(strings.Split(m[1], ",")[0]))
			length = max(n, 0)
		}
	}

	return family, length
}

// fitValue turns a made-up value into one the column takes: text for
// character columns, cut to their length, and the column's type otherwise.
func fitValue(dbType, colType, family string, length int) func(any) any {
	convert := importValue(dbType, colType, false)

	return func(v any) any {
		if v == nil {
			return nil
		}

		switch family {
		case "varchar", "char", "text":
			var s string

			switch x := v.(type) {
			case string:
				s = x
			case time.Time:
				s = x.Format("2006-01-02 15:04:05")
			case []byte:
				s = fmt.Sprintf("%x", x)
			default:
				s = fmt.Sprint(x)
			}

			if length > 0 && utf8.RuneCountInString(s) > length {
				s = string([]rune(s)[:length])
			}

			return s
		case "bool":
			if s, ok := v.(string); ok {
				return convert(s)
			}

			if b, ok := v.(bool); ok {
				return boolFor(dbType, b)
			}

			return convert(v)
		case "json":
			return fmt.Sprint(v)
		}

		if b, ok := v.(bool); ok {
			if b {
				return int64(1)
			}

			return int64(0)
		}

		// SQLite has no date type; its driver would write Go's own format.
		if t, ok := v.(time.Time); ok && dbType == "sqlite" {
			if family == "date" {
				return t.Format("2006-01-02")
			}

			return t.Format("2006-01-02 15:04:05")
		}

		return convert(v)
	}
}

// ////////////////////////////////////////////
// guessing

var nameRules = []struct {
	names     []string
	contains  []string
	generator string
}{
	{names: []string{"uuid", "guid"}, contains: []string{"uuid", "guid"}, generator: "uuid"},
	{contains: []string{"email", "mail"}, generator: "email"},
	{names: []string{"firstname", "fname", "givenname", "forename"}, generator: "first_name"},
	{names: []string{"lastname", "lname", "surname", "familyname"}, generator: "last_name"},
	{names: []string{"username", "login", "nickname", "handle", "nick"}, generator: "username"},
	{names: []string{"name", "fullname", "contactname", "displayname", "customername", "authorname", "personname", "ownername"}, generator: "full_name"},
	{contains: []string{"password", "passwd", "pwd", "secret"}, generator: "password"},
	{contains: []string{"phone", "mobile", "gsm", "fax", "tel"}, generator: "phone"},
	{names: []string{"gender", "sex"}, generator: "gender"},
	{contains: []string{"company", "organization", "organisation", "employer", "vendor", "supplier"}, generator: "company"},
	{names: []string{"jobtitle", "job", "position", "occupation", "role"}, generator: "job_title"},
	{names: []string{"street", "streetaddress", "address1", "addressline1", "addressline"}, generator: "street"},
	{contains: []string{"city", "town"}, generator: "city"},
	{names: []string{"state", "province", "region", "county"}, generator: "state"},
	{names: []string{"countrycode", "countryiso", "iso2"}, generator: "country_code"},
	{contains: []string{"country", "nation"}, generator: "country"},
	{contains: []string{"zip", "postcode", "postalcode"}, generator: "postcode"},
	{contains: []string{"address"}, generator: "address"},
	{names: []string{"lat", "latitude"}, generator: "latitude"},
	{names: []string{"lon", "lng", "long", "longitude"}, generator: "longitude"},
	{contains: []string{"url", "website", "homepage", "link", "href", "avatar", "image", "photo", "picture"}, generator: "url"},
	{names: []string{"domain", "hostname", "host"}, generator: "domain"},
	{names: []string{"ip", "ipaddress", "ipv4", "clientip", "remoteip"}, generator: "ipv4"},
	{names: []string{"ipv6"}, generator: "ipv6"},
	{contains: []string{"macaddress"}, generator: "mac"},
	{names: []string{"mac"}, generator: "mac"},
	{contains: []string{"useragent"}, generator: "user_agent"},
	{names: []string{"slug", "permalink"}, generator: "slug"},
	{contains: []string{"color", "colour"}, generator: "color"},
	{contains: []string{"currency"}, generator: "currency_code"},
	{names: []string{"lang", "language", "locale"}, generator: "language"},
	{contains: []string{"creditcard", "cardnumber", "ccnumber", "pan"}, generator: "credit_card"},
	{names: []string{"code", "sku", "reference", "ref", "barcode", "serial", "serialnumber"}, generator: "code"},
	{contains: []string{"description", "comment", "note", "bio", "body", "content", "summary", "message", "text", "remark", "about"}, generator: "paragraph"},
	{names: []string{"title", "subject", "headline", "caption", "label"}, generator: "sentence"},
	{names: []string{"status", "type", "kind", "category", "tag", "level", "tier", "state"}, generator: "word"},
}

// GuessColumn picks a generator for a column from its name and type.
func GuessColumn(dbType string, c ColumnInfo, auto string, ref *GenRef, singleKey bool) GenColumn {
	family, length := columnShape(c.Type)
	out := GenColumn{Name: c.Name, Type: c.Type, Nullable: c.Nullable, Auto: auto}

	def := strings.ToLower(c.Default)
	extra := strings.ToLower(c.Extra)

	switch {
	case auto == "" && (strings.Contains(def, "nextval(") || strings.Contains(extra, "auto_increment")):
		out.Auto = "identity"
	case auto == "" && dbType == "sqlite" && c.PrimaryKey && singleKey && strings.EqualFold(strings.TrimSpace(c.Type), "integer"):
		out.Auto = "identity"
	case auto == "" && dbType == "mssql" && (family == "timestamp" && strings.Contains(strings.ToLower(c.Type), "timestamp") || strings.Contains(strings.ToLower(c.Type), "rowversion")):
		out.Auto = "generated"
	}

	if out.Auto != "" {
		out.Generator = "skip"

		return out
	}

	if ref != nil {
		out.Generator, out.Ref = "fk", ref

		return out
	}

	if c.PrimaryKey {
		switch family {
		case "bigint", "int", "smallint", "decimal":
			out.Generator = "sequence"
		case "uuid":
			out.Generator = "uuid"
		default:
			if length == 0 || length >= 36 {
				out.Generator = "uuid"
			} else {
				out.Generator = "sequence"
			}
		}

		return out
	}

	// A default the database fills in, such as now() or 'active', is kept.
	if strings.TrimSpace(c.Default) != "" && !strings.EqualFold(strings.TrimSpace(c.Default), "null") {
		out.Generator = "skip"

		return out
	}

	name := normalizeName(c.Name)

	switch family {
	case "bool":
		out.Generator = "bool"
	case "uuid":
		out.Generator = "uuid"
	case "json":
		out.Generator = "json"
	case "binary":
		out.Generator = "bytes"
	case "time":
		out.Generator = "time"
	case "date", "timestamp", "timestamptz":
		out.Generator = "timestamp"
		if family == "date" {
			out.Generator = "date"
		}

		if strings.Contains(name, "birth") || name == "dob" || name == "bday" {
			out.Generator, out.From, out.To = "date", "1950-01-01", "2005-12-31"
		}
	case "bigint", "int", "smallint":
		out.Generator = "int"

		switch {
		case name == "age":
			out.Min, out.Max = ptr(18), ptr(90)
		case strings.Contains(name, "year"):
			out.Min, out.Max = ptr(1970), ptr(float64(time.Now().Year()))
		case anyContains(name, "quantity", "qty", "count", "stock", "amount"):
			out.Min, out.Max = ptr(0), ptr(100)
		case anyContains(name, "rating", "score", "stars"):
			out.Min, out.Max = ptr(1), ptr(5)
		}
	case "decimal", "real", "double":
		out.Generator = "decimal"

		switch { //nolint:staticcheck
		case name == "lat" || name == "latitude":
			out.Generator = "latitude"
		case name == "lon" || name == "lng" || name == "longitude":
			out.Generator = "longitude"
		default:
			if p, s, ok := precision(c.Type); ok {
				out.Decimals = &s
				out.Max = ptr(math.Min(1000, math.Pow(10, float64(p-s))-1))
			}
		}
	default:
		out.Generator = guessText(name, length)
	}

	return out
}

func guessText(name string, length int) string {
	for _, r := range nameRules {
		for _, n := range r.names {
			if name == n {
				return fitText(r.generator, length)
			}
		}

		for _, n := range r.contains {
			if strings.Contains(name, n) {
				return fitText(r.generator, length)
			}
		}
	}

	switch {
	case length > 0 && length <= 12:
		return "word"
	case length > 0 && length <= 60:
		return "words"
	}

	return "sentence"
}

// fitText swaps a generator for a shorter one where the column is narrow.
func fitText(gen string, length int) string {
	switch {
	case length == 0:
		return gen
	case gen == "country" && length <= 3, gen == "language" && length <= 2:
		return "country_code"
	case gen == "paragraph" && length < 200:
		return "sentence"
	case gen == "uuid" && length < 36:
		return "code"
	}

	return gen
}

func normalizeName(s string) string {
	s = strings.ToLower(s)

	var b strings.Builder

	for _, r := range s {
		if r != '_' && r != '-' && r != ' ' {
			b.WriteRune(r)
		}
	}

	return b.String()
}

func anyContains(s string, parts ...string) bool {
	for _, p := range parts {
		if strings.Contains(s, p) {
			return true
		}
	}

	return false
}

func precision(typ string) (int, int, bool) {
	m := typeArgs.FindStringSubmatch(typ)
	if m == nil {
		return 0, 0, false
	}

	parts := strings.Split(m[1], ",")

	p, err := strconv.Atoi(strings.TrimSpace(parts[0]))
	if err != nil || p <= 0 {
		return 0, 0, false
	}

	s := 0
	if len(parts) > 1 {
		s, _ = strconv.Atoi(strings.TrimSpace(parts[1]))
	}

	return p, min(max(s, 0), p), true
}

func ptr(f float64) *float64 { return &f }

func fkFor(fks []ForeignKey, column, schema string) *GenRef {
	for _, fk := range fks {
		if len(fk.Columns) != 1 || len(fk.RefColumns) != 1 || !strings.EqualFold(fk.Columns[0], column) {
			continue
		}

		refSchema := fk.RefSchema
		if refSchema == "" {
			refSchema = schema
		}

		return &GenRef{Schema: refSchema, Table: fk.RefTable, Column: fk.RefColumns[0]}
	}

	return nil
}

// ////////////////////////////////////////////
// reading the database

// autoColumns returns the columns the database fills itself: "identity"
// for numbered ones, "generated" for computed ones that cannot be written.
// It is best effort; a database it cannot ask gets the guesses alone.
func autoColumns(ctx context.Context, d Driver, q Querier, schema, table string) map[string]string {
	var stmt string

	args := []any{schema, table}

	switch d.Type() {
	case "postgres":
		stmt = `SELECT a.attname, CASE WHEN a.attgenerated = 's' OR a.attidentity = 'a' THEN 'generated' ELSE 'identity' END
FROM pg_catalog.pg_attribute a
JOIN pg_catalog.pg_class c ON c.oid = a.attrelid
JOIN pg_catalog.pg_namespace n ON n.oid = c.relnamespace
WHERE n.nspname = $1 AND c.relname = $2 AND a.attnum > 0 AND NOT a.attisdropped AND (a.attidentity IN ('a', 'd') OR a.attgenerated = 's')`
	case "mysql", "mariadb":
		stmt = `SELECT COLUMN_NAME, CASE WHEN LOWER(EXTRA) LIKE '%auto_increment%' THEN 'identity' ELSE 'generated' END
FROM information_schema.COLUMNS WHERE TABLE_SCHEMA = ? AND TABLE_NAME = ?
AND (LOWER(EXTRA) LIKE '%auto_increment%' OR LOWER(EXTRA) LIKE '%virtual generated%' OR LOWER(EXTRA) LIKE '%stored generated%')`
	case "mssql":
		stmt = `SELECT c.name, 'generated' FROM sys.columns c WHERE c.object_id = ` + mssqlObject + ` AND (c.is_identity = 1 OR c.is_computed = 1)`
	case "oracle":
		stmt = `SELECT column_name, CASE WHEN virtual_column = 'YES' THEN 'generated' ELSE 'identity' END
FROM all_tab_cols WHERE owner = :1 AND table_name = :2 AND hidden_column = 'NO' AND (identity_column = 'YES' OR virtual_column = 'YES')`
	case "sqlite":
		stmt = `SELECT name, 'generated' FROM pragma_table_xinfo(?) WHERE hidden IN (2, 3)`
		args = []any{table}
	default:
		return nil
	}

	rows, err := q.QueryContext(ctx, stmt, args...)
	if err != nil {
		return nil
	}

	pairs, err := scanPairs(rows)
	if err != nil {
		return nil
	}

	out := make(map[string]string, len(pairs))
	for k, v := range pairs {
		out[strings.ToLower(k)] = v
	}

	return out
}

func maxOf(ctx context.Context, d Driver, q Querier, schema, table, column string) (int64, error) {
	rows, err := q.QueryContext(ctx, "SELECT MAX("+d.QuoteIdent(column)+") FROM "+qualify(d, schema, table))
	if err != nil {
		return 0, err
	}
	defer func() { _ = rows.Close() }()

	var v any

	if rows.Next() {
		if err := rows.Scan(&v); err != nil {
			return 0, err
		}
	}

	if b, ok := v.([]byte); ok {
		v = string(b)
	}

	return cast.ToInt64E(v)
}

// sampleColumn reads up to fkSampleRows distinct values of a column.
func sampleColumn(ctx context.Context, d Driver, q Querier, schema, table, column string) ([]any, error) {
	col := d.QuoteIdent(column)
	from := qualify(d, schema, table) + " WHERE " + col + " IS NOT NULL"

	var stmt string

	switch d.Dialect() {
	case DialectMSSQL:
		stmt = fmt.Sprintf("SELECT DISTINCT TOP %d %s FROM %s", fkSampleRows, col, from)
	case DialectOracle:
		stmt = fmt.Sprintf("SELECT DISTINCT %s FROM %s FETCH FIRST %d ROWS ONLY", col, from, fkSampleRows)
	default:
		stmt = fmt.Sprintf("SELECT DISTINCT %s FROM %s LIMIT %d", col, from, fkSampleRows)
	}

	rows, err := q.QueryContext(ctx, stmt)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	var out []any

	for rows.Next() {
		var v any
		if err := rows.Scan(&v); err != nil {
			return nil, err
		}

		if b, ok := v.([]byte); ok {
			v = string(b)
		}

		out = append(out, v)
	}

	return out, rows.Err()
}
