package cloudmodels

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/DimmKirr/devcell/internal/cache"
	"github.com/DimmKirr/devcell/internal/ollama"
)

// OpenRouterURL is the public OpenRouter models endpoint (no auth required).
const OpenRouterURL = "https://openrouter.ai/api/v1/models"

// openRouterResponse is the top-level response from GET /v1/models.
type openRouterResponse struct {
	Data []openRouterModel `json:"data"`
}

type openRouterModel struct {
	ID      string            `json:"id"`
	Name    string            `json:"name"`
	Pricing openRouterPricing `json:"pricing"`
}

type openRouterPricing struct {
	Prompt     string `json:"prompt"`
	Completion string `json:"completion"`
}

// OpenRouterCacheTTL is the on-disk cache lifetime for OpenRouter model listings.
const OpenRouterCacheTTL = time.Hour

// FetchProviderModels fetches all models from OpenRouter and converts them
// to ollama.Model so they can be ranked alongside local models.
// Results are cached on-disk for 1 hour.
func FetchProviderModels(ctx context.Context, baseURL string) ([]ollama.Model, error) {
	const cacheKey = "openrouter-models.json"
	if models, ok := cache.Load[[]ollama.Model](cacheKey, OpenRouterCacheTTL); ok {
		return models, nil
	}

	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, baseURL, nil)
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("HTTP-Referer", "https://github.com/DimmKirr/devcell")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("fetch openrouter models: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("openrouter returned %d", resp.StatusCode)
	}

	var body openRouterResponse
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return nil, fmt.Errorf("decode openrouter response: %w", err)
	}

	models := make([]ollama.Model, 0, len(body.Data))
	for _, m := range body.Data {
		provider := extractProvider(m.ID)
		completionPrice := parsePrice(m.Pricing.Completion)
		models = append(models, ollama.Model{
			Name:                    m.ID,
			Provider:                provider,
			CompletionPricePerToken: completionPrice,
		})
	}
	cache.Save(cacheKey, models)
	return models, nil
}

// TrustedProviders lists the cloud providers shown in `cell models`.
// Restricted to the major frontier labs to keep the list focused.
var TrustedProviders = []string{"anthropic", "openai", "google"}

// FilterTrustedProviders removes models whose provider is not in TrustedProviders.
func FilterTrustedProviders(models []ollama.Model) []ollama.Model {
	trusted := make(map[string]bool, len(TrustedProviders))
	for _, p := range TrustedProviders {
		trusted[p] = true
	}
	out := make([]ollama.Model, 0, len(models))
	for _, m := range models {
		if trusted[m.Provider] {
			out = append(out, m)
		}
	}
	return out
}

// FilterLatestGen keeps only the highest-version model per family.
// Family is derived by stripping trailing version numbers from the model name part.
// E.g. "claude-opus-4-5" and "claude-opus-4" → family "claude-opus", keep "claude-opus-4-5".
func FilterLatestGen(models []ollama.Model) []ollama.Model {
	type familyEntry struct {
		model   ollama.Model
		version []int
	}

	byFamily := make(map[string]familyEntry)
	for _, m := range models {
		family, version := parseModelFamily(m.Name)
		if existing, ok := byFamily[family]; !ok || versionGreater(version, existing.version) {
			byFamily[family] = familyEntry{model: m, version: version}
		}
	}

	result := make([]ollama.Model, 0, len(byFamily))
	for _, entry := range byFamily {
		result = append(result, entry.model)
	}
	sort.Slice(result, func(i, j int) bool {
		return result[i].Name < result[j].Name
	})
	return result
}

// versionTrailingRe matches trailing version parts like "-4-5", "-4", or "-4.6".
var versionTrailingRe = regexp.MustCompile(`([-\.]\d+)+$`)

// versionSepRe splits version strings on "-" or ".".
var versionSepRe = regexp.MustCompile(`[-\.]`)

// parseModelFamily splits "provider/model-name-4-5" into family="provider/model-name"
// and version=[4,5]. Handles both dash-separated (-4-5) and dot-separated (.6) versions.
// E.g. "anthropic/claude-opus-4.6" → family="anthropic/claude-opus", version=[4,6].
func parseModelFamily(id string) (family string, version []int) {
	loc := versionTrailingRe.FindStringIndex(id)
	if loc == nil {
		return id, nil
	}
	family = id[:loc[0]]
	for _, part := range versionSepRe.Split(id[loc[0]:], -1) {
		n, err := strconv.Atoi(part)
		if err == nil {
			version = append(version, n)
		}
	}
	return family, version
}

// versionGreater returns true if version a > b lexicographically.
func versionGreater(a, b []int) bool {
	for i := 0; i < len(a) && i < len(b); i++ {
		if a[i] != b[i] {
			return a[i] > b[i]
		}
	}
	return len(a) > len(b)
}

func extractProvider(id string) string {
	if idx := strings.Index(id, "/"); idx >= 0 {
		return id[:idx]
	}
	return ""
}

func parsePrice(s string) float64 {
	s = strings.TrimSpace(s)
	if s == "" || s == "0" {
		return 0
	}
	v, err := strconv.ParseFloat(s, 64)
	if err != nil {
		return 0
	}
	return v
}
