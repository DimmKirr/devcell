package ollama

import (
	"context"
	"fmt"
	"math"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"

	ollamaapi "github.com/ollama/ollama/api"
)

// Model represents a locally available ollama model.
type Model struct {
	Name                    string
	Size                    int64
	ParameterSize           string
	Family                  string
	Provider                string  // "" or "ollama" = local; otherwise cloud provider (e.g. "anthropic")
	CompletionPricePerToken float64 // USD per token; 0 for local models
}

// RankedModel is a Model with its SWE-bench score and rank position.
type RankedModel struct {
	Model
	SWEScore         float64
	Rank             int
	ScoreSource      string  // "SWE", "est", or "" (no score)
	SpeedTPM         float64 // estimated tokens per minute
	RecommendedScore float64 // composite score for default ranking
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

// RankModels sorts models by composite score (descending) and limits to top N.
// It tries multiple matching strategies in order:
//  1. Direct family match against sweScores (MatchModelScore)
//  2. HF repo ID match against sweScores (FindSWEScore via hfInfoMap)
//  3. Cloud model NormalizeCloudID match against sweScores
//  4. Hardcoded fallback ratings
//
// ScoreSource is set to "SWE" for live matches, "est" for fallback, "" for no score.
// sortBy can be "swe", "speed", "size", or "" / "recommended".
func RankModels(models []Model, limit int, sweScores map[string]float64, hfInfoMap map[string]HFModelInfo, systemRAMGB float64, sortBy string) []RankedModel {
	if len(models) == 0 {
		return nil
	}

	bandwidthGBs := DetectAppleSiliconBandwidthGBs()

	ranked := make([]RankedModel, len(models))
	for i, m := range models {
		score, source := resolveScore(m, sweScores, hfInfoMap)
		speed := resolveSpeed(m, bandwidthGBs)
		ramFit := ramFitMultiplier(m, systemRAMGB)
		ranked[i] = RankedModel{
			Model:            m,
			SWEScore:         score,
			ScoreSource:      source,
			SpeedTPM:         speed,
			RecommendedScore: ComputeRecommendedScore(score, speed, ramFit),
		}
	}

	sort.Slice(ranked, func(i, j int) bool {
		switch sortBy {
		case "swe":
			if ranked[i].SWEScore != ranked[j].SWEScore {
				return ranked[i].SWEScore > ranked[j].SWEScore
			}
		case "speed":
			if ranked[i].SpeedTPM != ranked[j].SpeedTPM {
				return ranked[i].SpeedTPM > ranked[j].SpeedTPM
			}
		case "size":
			si := ParseParamSize(ranked[i].ParameterSize)
			sj := ParseParamSize(ranked[j].ParameterSize)
			if si != sj {
				return si > sj
			}
		default: // "recommended" or ""
			if ranked[i].RecommendedScore != ranked[j].RecommendedScore {
				return ranked[i].RecommendedScore > ranked[j].RecommendedScore
			}
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

	// Strategy 2b: for cloud models, try live SWE-bench scores first (exact
	// normalized-ID match), then fall back to hardcoded ratings.
	// Live scores always take priority over estimates.
	if m.Provider != "" && m.Provider != "ollama" {
		normalized := NormalizeCloudID(m.Name)
		if sweScores != nil {
			if s, ok := sweScores[normalized]; ok {
				return s, "SWE"
			}
		}
		if s, ok := lookupCloudRating(normalized); ok {
			return s, "est"
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

// ComputeRecommendedScore computes the composite ranking score.
// swe: SWE-bench %, speedTPM: tokens/min, ramFit: graduated multiplier (0–1).
//
// SWE quality dominates (90% weight). Speed contributes a small bonus (up to 5
// points) so that among models with equal SWE scores, faster wins — but a model
// with 8% lower SWE cannot overcome a 3-point speed bonus.
func ComputeRecommendedScore(swe, speedTPM, ramFit float64) float64 {
	speedBonus := math.Min(speedTPM/6000, 5) // max +5 pts at ≥30K T/m
	return (swe*0.90 + speedBonus) * ramFit
}

// ramFitMultiplier returns a graduated score based on how much of available RAM the model uses.
// Cloud models always return 1.0 (they need no local RAM).
// For local models, the multiplier decreases as RAM usage increases:
//
//	≤ 60% of RAM → 1.0  (green: comfortable, leaves room for OS + KV cache)
//	≤ 75% of RAM → 0.9  (yellow: safe, within conservative threshold)
//	≤ 90% of RAM → 0.5  (orange: tight, memory pressure likely)
//	≤ 100% of RAM → 0.1 (red: technically fits but risky)
//	> 100% of RAM → 0.0 (won't fit)
//
// The 60% sweet spot follows the consensus that ~40% of RAM should remain free
// for the OS, KV cache growth, and other processes during inference.
func ramFitMultiplier(m Model, systemRAMGB float64) float64 {
	if m.Provider != "" && m.Provider != "ollama" {
		return 1.0 // cloud models need 0 RAM
	}
	if systemRAMGB <= 0 {
		return 1.0 // no RAM info
	}
	paramsB := ParseParamSize(m.ParameterSize)
	if paramsB == 0 {
		return 1.0 // unknown size — can't penalise
	}
	needed := EstimateRAMGB(paramsB)
	ratio := needed / systemRAMGB
	switch {
	case ratio <= 0.60:
		return 1.0
	case ratio <= 0.75:
		return 0.9
	case ratio <= 0.90:
		return 0.5
	case ratio <= 1.00:
		return 0.1
	default:
		return 0.0
	}
}

// resolveSpeed estimates tokens/min for a model.
// bandwidthGBs is the detected Apple Silicon memory bandwidth (0 on other platforms).
func resolveSpeed(m Model, bandwidthGBs float64) float64 {
	if m.Provider != "" && m.Provider != "ollama" {
		return EstimateCloudSpeedTPM(m.CompletionPricePerToken)
	}
	return EstimateLocalSpeedTPM(ParseParamSize(m.ParameterSize), bandwidthGBs)
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
