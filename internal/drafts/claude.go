package drafts

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// Claude drafts posts with the Anthropic Messages API. It only writes words: the checker still decides.
type Claude struct {
	APIKey  string
	Model   string // e.g. claude-haiku-5-5
	BaseURL string // https://api.anthropic.com by default
	Client  *http.Client
}

const instructions = `You write the first line customers read on a status page during an incident.
Write one or two short sentences in plain English for the facts in <facts>.
Name only the components listed there. Do not say when it will be fixed, do not guess a cause,
and do not mention hosts, servers or internal systems. Reply with the post text only.`

// Draft asks the model for words.
func (c Claude) Draft(ctx context.Context, f Facts) (string, error) {
	if c.APIKey == "" {
		return "", fmt.Errorf("no API key: set ANTHROPIC_API_KEY or leave the template drafter on")
	}
	facts, _ := json.Marshal(map[string]any{
		"kind": f.Kind, "components": f.Names(), "regions": regionNames(f.Regions),
		"started_utc": f.Started.UTC().Format("15:04"),
	})
	body, _ := json.Marshal(map[string]any{
		"model":      c.model(),
		"max_tokens": 200,
		"system":     instructions,
		"messages": []map[string]any{{
			"role": "user", "content": "<facts>" + string(facts) + "</facts>",
		}},
	})
	base := c.BaseURL
	if base == "" {
		base = "https://api.anthropic.com"
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, base+"/v1/messages", bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	req.Header.Set("content-type", "application/json")
	req.Header.Set("x-api-key", c.APIKey)
	req.Header.Set("anthropic-version", "2023-06-01")
	client := c.Client
	if client == nil {
		client = &http.Client{Timeout: 20 * time.Second}
	}
	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("the model API answered %d: %s", resp.StatusCode, strings.TrimSpace(string(raw)))
	}
	var out struct {
		Content []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content"`
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		return "", err
	}
	var b strings.Builder
	for _, part := range out.Content {
		if part.Type == "text" {
			b.WriteString(part.Text)
		}
	}
	return strings.TrimSpace(b.String()), nil
}

func (c Claude) model() string {
	if c.Model != "" {
		return c.Model
	}
	return "claude-haiku-5-5"
}

func regionNames(locs []string) []string {
	out := make([]string, 0, len(locs))
	for _, l := range locs {
		out = append(out, RegionName(l))
	}
	return out
}
