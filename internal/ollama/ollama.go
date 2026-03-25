package ollama

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"

	ollamaapi "github.com/ollama/ollama/api"
)

// Model represents a locally available ollama model.
type Model struct {
	Name          string
	Size          int64
	ParameterSize string
	Family        string
}

// RankedModel is a Model with its SWE-bench score and rank position.
type RankedModel struct {
	Model
	SWEScore    float64
	Rank        int
	ScoreSource string // "SWE", "est", or "" (no score)
}

// DefaultBaseURL is the default ollama API endpoint.
const DefaultBaseURL = "http://localhost:11434"

// Detect checks if ollama is reachable at the given base URL.
func Detect(ctx context.Context, baseURL string) bool {
	ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, baseURL, nil)
	if err != nil {
		return false
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return false
	}
	resp.Body.Close()
	return resp.StatusCode == http.StatusOK
}

// FetchModels retrieves the list of locally available models from ollama
// using the official Go SDK.
func FetchModels(ctx context.Context, baseURL string) ([]Model, error) {
	u, err := url.Parse(baseURL)
	if err != nil {
		return nil, fmt.Errorf("parse ollama URL: %w", err)
	}

	client := ollamaapi.NewClient(u, http.DefaultClient)
	resp, err := client.List(ctx)
	if err != nil {
		return nil, fmt.Errorf("ollama list: %w", err)
	}

	models := make([]Model, len(resp.Models))
	for i, m := range resp.Models {
		models[i] = Model{
			Name:          m.Name,
			Size:          m.Size,
			ParameterSize: m.Details.ParameterSize,
			Family:        m.Details.Family,
		}
	}
	return models, nil
}

// RankModels sorts models by SWE-bench score (descending) and limits to top N.
// It tries multiple matching strategies in order:
//  1. Direct family match against sweScores (MatchModelScore)
//  2. HF repo ID match against sweScores (FindSWEScore via hfInfoMap)
//  3. Hardcoded fallback ratings
//
// ScoreSource is set to "SWE" for live matches, "est" for fallback, "" for no score.
func RankModels(models []Model, limit int, sweScores map[string]float64, hfInfoMap map[string]HFModelInfo) []RankedModel {
	if len(models) == 0 {
		return nil
	}

	ranked := make([]RankedModel, len(models))
	for i, m := range models {
		score, source := resolveScore(m, sweScores, hfInfoMap)
		ranked[i] = RankedModel{
			Model:       m,
			SWEScore:    score,
			ScoreSource: source,
		}
	}

	sort.Slice(ranked, func(i, j int) bool {
		if ranked[i].SWEScore != ranked[j].SWEScore {
			return ranked[i].SWEScore > ranked[j].SWEScore
		}
		return ranked[i].Name < ranked[j].Name
	})

	if limit > 0 && len(ranked) > limit {
		ranked = ranked[:limit]
	}

	for i := range ranked {
		ranked[i].Rank = i + 1
	}

	return ranked
}

// resolveScore tries to find a score for a model using multiple strategies.
func resolveScore(m Model, sweScores map[string]float64, hfInfoMap map[string]HFModelInfo) (float64, string) {
	family := modelFamily(m.Name)

	// Strategy 1: direct family match against live SWE-bench scores.
	if sweScores != nil {
		if s, ok := MatchModelScore(m.Name, sweScores); ok {
			return s, "SWE"
		}
	}

	// Strategy 2: use HF repo ID to match SWE-bench scores.
	if sweScores != nil && hfInfoMap != nil {
		if info, ok := hfInfoMap[family]; ok && info.ModelID != "" {
			if s, found := FindSWEScore(sweScores, info.ModelID); found {
				return s, "SWE"
			}
		}
	}

	// Strategy 3: hardcoded fallback (exact name match).
	if s := sweBenchRatings[m.Name]; s > 0 {
		return s, "est"
	}

	// Strategy 4: loose fallback — match by family:size prefix.
	// e.g. "qwen3-coder:30b-128k" matches "qwen3-coder:30b"
	if s := matchFallbackScore(m.Name); s > 0 {
		return s, "est"
	}

	return 0, ""
}

// FormatTOMLSnippet generates a commented-out TOML snippet for devcell.toml
// from ranked models.
func FormatTOMLSnippet(ranked []RankedModel) string {
	if len(ranked) == 0 {
		return ""
	}

	var names []string
	for _, r := range ranked {
		names = append(names, fmt.Sprintf("%q", r.Name))
	}

	var b strings.Builder
	b.WriteString(fmt.Sprintf("# [models]\n"))
	b.WriteString(fmt.Sprintf("# default = \"ollama/%s\"\n", ranked[0].Name))
	b.WriteString("# [models.providers.ollama]\n")
	b.WriteString(fmt.Sprintf("# models = [%s]\n", strings.Join(names, ", ")))

	return b.String()
}

// FormatActiveTOMLSnippet generates an active (uncommented) TOML snippet
// for devcell.toml from ranked models. The #1 ranked model becomes the default.
func FormatActiveTOMLSnippet(ranked []RankedModel) string {
	if len(ranked) == 0 {
		return ""
	}

	var names []string
	for _, r := range ranked {
		names = append(names, fmt.Sprintf("%q", r.Name))
	}

	var b strings.Builder
	b.WriteString("[llm.models]\n")
	b.WriteString(fmt.Sprintf("default = \"ollama/%s\"\n", ranked[0].Name))
	b.WriteString("\n[llm.models.providers.ollama]\n")
	b.WriteString(fmt.Sprintf("models = [%s]\n", strings.Join(names, ", ")))

	return b.String()
}
