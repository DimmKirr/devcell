package ollama

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/DimmKirr/devcell/internal/cache"
)

// SWEBenchURL is the default URL for SWE-bench leaderboard data.
const SWEBenchURL = "https://raw.githubusercontent.com/SWE-bench/swe-bench.github.io/master/data/leaderboards.json"

// leaderboardsData is the top-level JSON structure from swe-bench.
type leaderboardsData struct {
	Leaderboards []leaderboard `json:"leaderboards"`
}

type leaderboard struct {
	Name    string    `json:"name"`
	Results []lbEntry `json:"results"`
}

type lbEntry struct {
	Name     string   `json:"name"`
	Resolved float64  `json:"resolved"`
	OSModel  bool     `json:"os_model"`
	Tags     []string `json:"tags"`
}

// FetchSWEBenchScores fetches the SWE-bench Verified leaderboard and returns
// the best score per model. Keys include lowercased model tags, HuggingFace
// repo paths extracted from URL tags, and model name parts.
//
// All entries are included (not filtered by os_model) so that HF repo ID
// matching can find scores for any model.
// SWEBenchCacheTTL is the on-disk cache lifetime for SWE-bench leaderboard data.
const SWEBenchCacheTTL = 6 * time.Hour

func FetchSWEBenchScores(ctx context.Context, url string) (map[string]float64, error) {
	const cacheKey = "swebench-scores.json"
	if scores, ok := cache.Load[map[string]float64](cacheKey, SWEBenchCacheTTL); ok {
		return scores, nil
	}

	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("fetch swe-bench data: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("swe-bench returned status %d", resp.StatusCode)
	}

	var data leaderboardsData
	if err := json.NewDecoder(resp.Body).Decode(&data); err != nil {
		return nil, fmt.Errorf("decode swe-bench JSON: %w", err)
	}

	// Find the "Verified" leaderboard.
	var verified *leaderboard
	for i := range data.Leaderboards {
		if strings.Contains(strings.ToLower(data.Leaderboards[i].Name), "verified") {
			verified = &data.Leaderboards[i]
			break
		}
	}
	if verified == nil {
		return nil, fmt.Errorf("no Verified leaderboard found in data")
	}

	// Extract best score per model, stored under multiple keys for flexible matching.
	scores := make(map[string]float64)
	for _, entry := range verified.Results {
		modelTag := extractModelTag(entry.Tags)
		if modelTag == "" {
			continue
		}

		// Store under the raw tag (lowercased).
		storeScore(scores, strings.ToLower(modelTag), entry.Resolved)

		// If the tag is a HuggingFace URL, also store under the repo path.
		// e.g. "https://huggingface.co/Qwen/Qwen3-Coder-30B-A3B-Instruct"
		//   → "qwen/qwen3-coder-30b-a3b-instruct"
		//   → "qwen3-coder-30b-a3b-instruct"
		if strings.Contains(modelTag, "huggingface.co/") {
			parts := strings.SplitN(modelTag, "huggingface.co/", 2)
			if len(parts) == 2 {
				repoPath := strings.ToLower(strings.TrimRight(parts[1], "/"))
				storeScore(scores, repoPath, entry.Resolved)
				// Also store just the model name (after org/).
				if slashIdx := strings.Index(repoPath, "/"); slashIdx >= 0 {
					storeScore(scores, repoPath[slashIdx+1:], entry.Resolved)
				}
			}
		}
	}

	cache.Save(cacheKey, scores)
	return scores, nil
}

// storeScore stores score under key, keeping the best (highest) value.
func storeScore(m map[string]float64, key string, score float64) {
	if score > m[key] {
		m[key] = score
	}
}

// extractModelTag finds "Model: xxx" in the tags array and returns xxx.
func extractModelTag(tags []string) string {
	for _, tag := range tags {
		if strings.HasPrefix(tag, "Model: ") {
			return strings.TrimPrefix(tag, "Model: ")
		}
	}
	return ""
}

// FindSWEScore finds the best SWE-bench score for a HuggingFace repo ID.
// It checks the scores map using the full repo ID, the model name part,
// and case-insensitive variants.
func FindSWEScore(scores map[string]float64, hfRepoID string) (float64, bool) {
	if scores == nil || hfRepoID == "" {
		return 0, false
	}

	id := strings.ToLower(hfRepoID)

	// Try full repo path: "qwen/qwen2.5-coder-32b-instruct"
	if score, ok := scores[id]; ok {
		return score, true
	}

	// Try model name part only: "qwen2.5-coder-32b-instruct"
	if slashIdx := strings.Index(id, "/"); slashIdx >= 0 {
		modelName := id[slashIdx+1:]
		if score, ok := scores[modelName]; ok {
			return score, true
		}
	}

	// Try substring scan: does any SWE-bench key contain the model name?
	modelName := id
	if slashIdx := strings.Index(id, "/"); slashIdx >= 0 {
		modelName = id[slashIdx+1:]
	}
	for key, score := range scores {
		if strings.Contains(key, modelName) || strings.Contains(modelName, key) {
			return score, true
		}
	}

	return 0, false
}

// MatchModelScore finds the SWE-bench score for an ollama model name.
// It strips the :size suffix and does case-insensitive matching.
func MatchModelScore(ollamaName string, scores map[string]float64) (float64, bool) {
	if scores == nil {
		return 0, false
	}

	family := modelFamily(ollamaName)

	// Try exact match (lowercase).
	key := strings.ToLower(family)
	if score, ok := scores[key]; ok {
		return score, true
	}

	// Try case-insensitive scan (handles mixed-case keys).
	for k, score := range scores {
		if strings.EqualFold(k, family) {
			return score, true
		}
	}

	return 0, false
}

// modelFamily strips the :tag suffix from an ollama model name.
// "deepseek-r1:32b" → "deepseek-r1", "codellama:latest" → "codellama".
func modelFamily(name string) string {
	if idx := strings.LastIndex(name, ":"); idx > 0 {
		return name[:idx]
	}
	return name
}

// NormalizeCloudID converts an OpenRouter model ID to a SWE-bench-comparable key.
// Strips provider prefix, converts dots to dashes, strips :variant suffixes.
// "anthropic/claude-opus-4.6" → "claude-opus-4-6"
func NormalizeCloudID(id string) string {
	// Strip provider prefix (OpenRouter IDs have exactly one '/').
	// e.g. "anthropic/claude-opus-4.6" → "claude-opus-4.6"
	if idx := strings.LastIndex(id, "/"); idx >= 0 {
		id = id[idx+1:]
	}
	// Strip :variant: "model:preview" → "model"
	if idx := strings.Index(id, ":"); idx >= 0 {
		id = id[:idx]
	}
	// Dots to dashes for SWE-bench key compatibility.
	return strings.ReplaceAll(id, ".", "-")
}

// matchFallbackScore finds a fallback rating for a model by checking if any
// rated model name is a prefix of the given name, or if they share the same
// family. Handles variants like "qwen3-coder:30b-128k" matching
// "qwen3-coder:30b".
func matchFallbackScore(name string) float64 {
	// Prefix match: "qwen3-coder:30b-128k" starts with "qwen3-coder:30b"
	var bestScore float64
	var bestLen int
	for rated, score := range sweBenchRatings {
		if strings.HasPrefix(name, rated) && len(rated) > bestLen {
			bestScore = score
			bestLen = len(rated)
		}
	}
	if bestScore > 0 {
		return bestScore
	}

	// Family match: pick the best score for the same family.
	family := modelFamily(name)
	for rated, score := range sweBenchRatings {
		if modelFamily(rated) == family && score > bestScore {
			bestScore = score
		}
	}
	return bestScore
}
