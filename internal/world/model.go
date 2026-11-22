package world

import (
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strings"
	"time"
)

// Phrases is the bank the stand-in model writes from. The held-out world uses a bank whose errors are worded
// in ways the dev bank never used, so the checker meets phrasings it was not tuned on.
type Phrases struct {
	Clean    []string            `json:"clean"`
	Resolved []string            `json:"resolved"`
	Errors   map[string][]string `json:"errors"`
}

// LoadPhrases reads a phrase bank.
func LoadPhrases(path string) (Phrases, error) {
	var p Phrases
	b, err := os.ReadFile(path)
	if err != nil {
		return p, err
	}
	return p, json.Unmarshal(b, &p)
}

// ModelDown is the stand-in model reading a check's recent runs and saying whether to page. It is quick when
// several locations fail, too eager when one location fails once, and sometimes slow to call recovery.
func (w *World) ModelDown(check string, at time.Time, fails, passes map[string]int, down bool) bool {
	m := w.Cfg.Model
	failing, allPass := 0, true
	for l, f := range fails {
		if f > 0 {
			failing++
		}
		if passes[l] == 0 {
			allPass = false
		}
	}
	u := w.u("judge", check, at.Unix()/60)
	if down {
		if allPass {
			return u >= m.UpAllPass
		}
		return true
	}
	switch {
	case failing >= 2:
		return u < m.DownTwoPlus
	case failing == 1:
		return u < m.DownOne
	}
	return false
}

// ModelDraft is the stand-in model writing a status post. Each error kind is drawn on its own, keyed by the
// incident and the kind of post; what it injected is recorded in Injected for the lab to score.
func (w *World) ModelDraft(incident, kind string, names, others, regions []string, service string) string {
	pb := w.phrases
	sort.Strings(names)
	pick := func(xs []string, key ...any) string {
		if len(xs) == 0 {
			return ""
		}
		return xs[int(w.u(append([]any{"phrase", incident, kind}, key...)...)*float64(len(xs)))%len(xs)]
	}
	fill := func(s string, comps []string) string {
		region := ""
		if len(regions) > 0 {
			region = " for customers in " + joinNames(regions)
		}
		host := service + "-1.prod.internal"
		if hs := w.svcOf[service].Hosts; len(hs) > 0 {
			host = hs[0]
		}
		verb := "is"
		if len(comps) > 1 {
			verb = "are"
		}
		r := strings.NewReplacer("{components}", joinNames(comps), "{is}", verb, "{region}", region,
			"{host}", host, "{service}", service)
		return r.Replace(s)
	}
	if kind == "resolved" {
		text := fill(pick(pb.Resolved), names)
		w.Injected[text] = nil
		return text
	}
	var injected []string
	comps := names
	errKinds := make([]string, 0, len(w.Cfg.Model.Errors))
	for k := range w.Cfg.Model.Errors {
		errKinds = append(errKinds, k)
	}
	sort.Strings(errKinds)
	has := map[string]bool{}
	for _, k := range errKinds {
		if w.u("err", incident, kind, k) < w.Cfg.Model.Errors[k] && len(pb.Errors[k]) > 0 {
			has[k] = true
		}
	}
	if has["unaffected_component"] && len(others) > 0 {
		sort.Strings(others)
		extra := others[int(w.u("other", incident)*float64(len(others)))%len(others)]
		comps = append(append([]string(nil), names...), extra)
		injected = append(injected, "unaffected_component")
	}
	text := fill(pick(pb.Clean), comps)
	for _, k := range errKinds {
		if k == "unaffected_component" || !has[k] {
			continue
		}
		text += fill(pick(pb.Errors[k], k), comps)
		injected = append(injected, k)
	}
	if prev, ok := w.Injected[text]; ok && fmt.Sprint(prev) != fmt.Sprint(injected) {
		// The same words cannot carry two different truths; the first one stays.
		return text
	}
	w.Injected[text] = injected
	return text
}
