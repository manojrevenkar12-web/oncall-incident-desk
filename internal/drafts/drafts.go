// Package drafts writes status-page updates. A model may write the words; the checker decides whether they may
// be published: every component named must be affected, nothing may promise a recovery time or state a cause
// nobody has confirmed, and no internal detail may leak. A draft that fails is replaced by the safe template.
package drafts

import (
	"context"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/manojrevenkar12-web/oncall-incident-desk/internal/oncall"
)

// Facts are what the desk knows when it writes a post. They come from checks, not from the model.
type Facts struct {
	IncidentID string             `json:"incident_id"`
	Kind       string             `json:"kind"` // "investigating" or "resolved"
	Components []oncall.Component `json:"components"`
	Regions    []string           `json:"regions,omitempty"` // set when only some locations fail
	Started    time.Time          `json:"started"`
}

// Names returns the affected component names, sorted.
func (f Facts) Names() []string {
	out := make([]string, 0, len(f.Components))
	for _, c := range f.Components {
		out = append(out, c.Name)
	}
	sort.Strings(out)
	return out
}

// Drafter writes the words of a post. A language model, or the template.
type Drafter interface {
	Draft(ctx context.Context, f Facts) (string, error)
}

// Template is the drafter that never needs checking twice: it says only what the facts say.
type Template struct{}

func (Template) Draft(_ context.Context, f Facts) (string, error) { return Write(f), nil }

// Write renders the safe template.
func Write(f Facts) string {
	names := join(f.Names())
	if f.Kind == "resolved" {
		return fmt.Sprintf("%s %s working normally again. This incident is resolved.", names, verb(len(f.Components)))
	}
	where := ""
	if len(f.Regions) > 0 {
		rs := make([]string, 0, len(f.Regions))
		for _, r := range f.Regions {
			rs = append(rs, RegionName(r))
		}
		sort.Strings(rs)
		where = " for customers served from " + join(rs)
	}
	return fmt.Sprintf("We are investigating failures affecting %s%s since %s UTC. We will post the next update here as we learn more.",
		names, where, f.Started.UTC().Format("15:04"))
}

func verb(n int) string {
	if n == 1 {
		return "is"
	}
	return "are"
}

func join(xs []string) string {
	switch len(xs) {
	case 0:
		return "our service"
	case 1:
		return xs[0]
	}
	return strings.Join(xs[:len(xs)-1], ", ") + " and " + xs[len(xs)-1]
}

// RegionName turns a check location into the name customers know.
func RegionName(loc string) string {
	names := map[string]string{
		"eu-central-1":   "Frankfurt",
		"eu-west-2":      "London",
		"us-east-1":      "North Virginia",
		"ap-southeast-1": "Singapore",
	}
	if n, ok := names[loc]; ok {
		return n
	}
	return loc
}

// Violation is one reason a draft may not be published.
type Violation struct {
	Kind   string `json:"kind"`
	Detail string `json:"detail"`
}

// Kinds the checker reports.
const (
	UnaffectedComponent = "unaffected_component"
	MissingComponent    = "missing_component"
	RecoveryPromise     = "recovery_promise"
	UnconfirmedCause    = "unconfirmed_cause"
	InternalDetail      = "internal_detail"
	Shape               = "shape"
)

// Checker holds the catalogue facts the rules need.
type Checker struct {
	public []oncall.Component
	hosts  []string
	MaxLen int
}

// NewChecker builds a checker over the catalogue.
func NewChecker(ix *oncall.Index) *Checker {
	c := &Checker{MaxLen: 600}
	for _, p := range ix.Components {
		if p.Public {
			c.public = append(c.public, p)
		}
	}
	for _, s := range ix.Services {
		c.hosts = append(c.hosts, s.Hosts...)
	}
	return c
}

// The rules name categories, not phrasings: an investigating post may not refer to time or quantity at all
// (beyond the start time the desk writes itself), may not name a mechanism or a cause, and may not carry
// anything that looks like an internal name. A new wording of the same mistake still falls in the category.
var (
	timeWords = regexp.MustCompile(`(?i)\b(?:soon|shortly|momentarily|eta|minutes?|mins?|hours?|hrs?|lunch|tonight|today|tomorrow|morning|afternoon|evening|overnight|half|twenty|thirty|forty|fifteen|sixty|asap)\b`)
	digits    = regexp.MustCompile(`\d`)
	recovery  = regexp.MustCompile(`(?i)\b(?:expect(?:ed|s)?|should|will|plan(?:ned)?|aim|hope)\b[^.]{0,40}\b(?:fix(?:ed)?|resolved?|resolution|restored?|recover(?:y|ed)?|back|normal|return)\b`)
	causes    = regexp.MustCompile(`(?i)\b(?:because|due to|caused?|reason|behind (?:this|it|the)|trigger(?:ed)?|traced|stems?|follows|following a|failover|deploy(?:ment|ed)?|roll(?:out|ing back|ed back)|release|migration|certificate|config(?:uration)?|provider|upstream|network|database|dns|bug|regression|patch)\b`)
	internals = []*regexp.Regexp{
		regexp.MustCompile(`(?i)\b[a-z0-9-]+(?:\.[a-z0-9-]+)*\.(?:internal|local|svc|corp|cluster)\b`),
		regexp.MustCompile(`\b\d{1,3}\.\d{1,3}\.\d{1,3}\.\d{1,3}\b`),
		regexp.MustCompile(`\b(?:ECONNREFUSED|ETIMEDOUT|ECONNRESET|ENOTFOUND|EHOSTUNREACH|OOMKilled|CrashLoopBackOff)\b`),
		regexp.MustCompile(`(?i)\b(?:stack ?trace|pods?|kubernetes|k8s|replica|node|cluster|shard|pool)\b`),
		regexp.MustCompile(`(?i)\b[a-z]+(?:-[a-z0-9]+)*-\d+\b`),
	}
)

// Check returns every reason the text may not be published for these facts. No violations means publishable.
func (c *Checker) Check(text string, f Facts) []Violation {
	var out []Violation
	t := strings.TrimSpace(text)
	if t == "" || len(t) > c.MaxLen || strings.Contains(t, "\n\n\n") {
		out = append(out, Violation{Shape, fmt.Sprintf("%d characters", len(t))})
		return out
	}
	affected := map[string]bool{}
	for _, p := range f.Components {
		affected[p.ID] = true
	}
	named := 0
	for _, p := range c.public {
		if !mentions(t, p.Name) {
			continue
		}
		if affected[p.ID] {
			named++
		} else {
			out = append(out, Violation{UnaffectedComponent, p.Name})
		}
	}
	if named == 0 && len(f.Components) > 0 {
		out = append(out, Violation{MissingComponent, join(f.Names())})
	}
	if f.Kind != "resolved" {
		rest := strings.ReplaceAll(t, f.Started.UTC().Format("15:04")+" UTC", "")
		switch {
		case timeWords.MatchString(rest):
			out = append(out, Violation{RecoveryPromise, timeWords.FindString(rest)})
		case digits.MatchString(rest):
			out = append(out, Violation{RecoveryPromise, "a number: " + numberAround(rest)})
		case recovery.MatchString(rest):
			out = append(out, Violation{RecoveryPromise, recovery.FindString(rest)})
		}
	}
	if m := causes.FindString(t); m != "" {
		out = append(out, Violation{UnconfirmedCause, m})
	}
	for _, h := range c.hosts {
		if h != "" && strings.Contains(strings.ToLower(t), strings.ToLower(h)) {
			out = append(out, Violation{InternalDetail, h})
		}
	}
	for _, re := range internals {
		if m := re.FindString(t); m != "" {
			out = append(out, Violation{InternalDetail, m})
			break
		}
	}
	return out
}

func numberAround(s string) string {
	i := digits.FindStringIndex(s)
	lo, hi := i[0]-12, i[1]+12
	if lo < 0 {
		lo = 0
	}
	if hi > len(s) {
		hi = len(s)
	}
	return strings.TrimSpace(s[lo:hi])
}

// mentions reports a whole-word, case-insensitive mention of a component name.
func mentions(text, name string) bool {
	re := regexp.MustCompile(`(?i)(?:^|[^a-z0-9])` + regexp.QuoteMeta(name) + `(?:$|[^a-z0-9])`)
	return re.MatchString(text)
}

// Decide runs a drafter, checks its words and returns what to publish. When the check fails, or the drafter
// errs, the template is published and fallback is true. With check off the draft is published as written.
func Decide(ctx context.Context, d Drafter, c *Checker, f Facts, check bool) (text string, violations []Violation, fallback bool) {
	draft, err := d.Draft(ctx, f)
	if err != nil {
		return Write(f), []Violation{{Shape, "drafter error: " + err.Error()}}, true
	}
	if !check {
		return draft, nil, false
	}
	if v := c.Check(draft, f); len(v) > 0 {
		return Write(f), v, true
	}
	return draft, nil, false
}
