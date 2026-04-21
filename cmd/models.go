package main

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/DimmKirr/devcell/internal/cache"
	"github.com/DimmKirr/devcell/internal/cloudmodels"
	"github.com/DimmKirr/devcell/internal/ollama"
	"github.com/DimmKirr/devcell/internal/ux"
	"github.com/spf13/cobra"
)

// ModelEntry is the typed representation of a ranked model for JSON/YAML output.
type ModelEntry struct {
	Rank             int     `json:"rank"              yaml:"rank"`
	Name             string  `json:"name"              yaml:"name"`
	Provider         string  `json:"provider"          yaml:"provider"`
	SWEScore         float64 `json:"swe_score"         yaml:"swe_score"`
	Size             string  `json:"size"              yaml:"size"`
	Type             string  `json:"type"              yaml:"type"`
	SpeedTPM         float64 `json:"speed_tpm"         yaml:"speed_tpm"`
	Hardware         string  `json:"hardware"          yaml:"hardware"`
	RecommendedScore float64 `json:"recommended_score" yaml:"recommended_score"`
}

// Reuse shared styles from ux package.
var (
	modGray  = ux.StyleMuted
	modGreen = ux.StyleSuccess
	modRed   = ux.StyleError
	modBold  = ux.StyleBold
)

var modelsCmd = &cobra.Command{
	Use:   "models",
	Short: "Detect locally available LLM models and show coding capability ratings",
	Long: `Detects locally running ollama and lists available models ranked by
coding capability based on SWE-bench Verified leaderboard data.

Scores are fetched live from swebench.com and represent the best results
for each model family (full-precision, with agentic scaffolding).
Quantized ollama variants will score lower in practice.

Falls back to built-in estimates if SWE-bench data is unavailable.

Examples:

    cell models                  # cloud + local downloaded (default)
    cell models --source=local   # local ollama models only
    cell models --source=cloud   # cloud models only
    cell models --debug`,
	RunE: func(cmd *cobra.Command, args []string) error {
		applyOutputFlags()
		ctx := context.Background()
		baseURL := ollama.DefaultBaseURL

		source, _ := cmd.Flags().GetString("source")
		wantLocal := source == "local" || source == "all"
		wantCloud := source == "cloud" || source == "all"

		// Fetch local ollama models (best effort — command still shows cloud models without it).
		var models []ollama.Model
		ux.Debugf("→ GET %s  [%s]", baseURL, time.Now().Format("15:04:05.000"))
		t0 := time.Now()
		ollamaUp := wantLocal && ollama.Detect(ctx, baseURL)
		ux.Debugf("← GET %s  elapsed=%s  reachable=%v", baseURL, time.Since(t0).Round(time.Millisecond), ollamaUp)
		if ollamaUp {
			ux.Debugf("→ GET %s/api/tags  [%s]", baseURL, time.Now().Format("15:04:05.000"))
			t0 = time.Now()
			localModels, err := ollama.FetchModels(ctx, baseURL)
			if err != nil {
				return fmt.Errorf("fetch models: %w", err)
			}
			ux.Debugf("← GET %s/api/tags  elapsed=%s  items=%d", baseURL, time.Since(t0).Round(time.Millisecond), len(localModels))
			for _, m := range localModels {
				ux.Debugf("  %s (size=%s, family=%s)", m.Name, m.ParameterSize, m.Family)
			}
			models = append(models, localModels...)
		}

		// Fetch cloud models from OpenRouter (best effort).
		if wantCloud {
			ux.Debugf("→ GET %s  cached=%v  [%s]", cloudmodels.OpenRouterURL, cache.Has("openrouter-models.json", cloudmodels.OpenRouterCacheTTL), time.Now().Format("15:04:05.000"))
			t0 = time.Now()
			cloudRaw, cloudErr := cloudmodels.FetchProviderModels(ctx, cloudmodels.OpenRouterURL)
			if cloudErr != nil {
				ux.Debugf("← GET %s  elapsed=%s  error=%v", cloudmodels.OpenRouterURL, time.Since(t0).Round(time.Millisecond), cloudErr)
			} else {
				filtered := cloudmodels.FilterTrustedProviders(cloudmodels.FilterLatestGen(cloudRaw))
				ux.Debugf("← GET %s  elapsed=%s  items=%d  after_filter=%d", cloudmodels.OpenRouterURL, time.Since(t0).Round(time.Millisecond), len(cloudRaw), len(filtered))
				models = append(models, filtered...)
			}
		}

		if len(models) == 0 {
			switch source {
			case "local":
				ux.Warn("No local models found. Is ollama running? Try: ollama serve")
			case "cloud":
				ux.Warn("No cloud models found (OpenRouter unavailable).")
			default:
				ux.Warn("No models found (ollama not running and OpenRouter unavailable).")
			}
			return nil
		}

		// Fetch live SWE-bench scores (falls back to hardcoded on failure).
		var sweScores map[string]float64
		ux.Debugf("→ GET %s  cached=%v  [%s]", ollama.SWEBenchURL, cache.Has("swebench-scores.json", ollama.SWEBenchCacheTTL), time.Now().Format("15:04:05.000"))
		t0 = time.Now()
		sweScores, sweErr := ollama.FetchSWEBenchScores(ctx, ollama.SWEBenchURL)
		if sweErr != nil {
			ux.Debugf("← GET %s  elapsed=%s  error=%v", ollama.SWEBenchURL, time.Since(t0).Round(time.Millisecond), sweErr)
		} else {
			ux.Debugf("← GET %s  elapsed=%s  items=%d", ollama.SWEBenchURL, time.Since(t0).Round(time.Millisecond), len(sweScores))
		}

		// Fetch HuggingFace model info for local ollama models only (best effort).
		// Cloud models (Provider != "") are not on HuggingFace, so skip them.
		hfInfoMap := make(map[string]ollama.HFModelInfo)
		for _, m := range models {
			if m.Provider != "" && m.Provider != "ollama" {
				continue // skip cloud models — not on HuggingFace
			}
			family := ollama.ModelFamily(m.Name)
			if _, done := hfInfoMap[family]; done {
				continue
			}
			ux.Debugf("→ GET %s/%s  [%s]", ollama.HuggingFaceAPIURL, family, time.Now().Format("15:04:05.000"))
			t0 = time.Now()
			info, hfErr := ollama.FetchHFModelInfo(ctx, ollama.HuggingFaceAPIURL, family)
			if hfErr != nil {
				ux.Debugf("← GET %s/%s  elapsed=%s  error=%v", ollama.HuggingFaceAPIURL, family, time.Since(t0).Round(time.Millisecond), hfErr)
				continue
			}
			hfInfoMap[family] = info
			ux.Debugf("← GET %s/%s  elapsed=%s  model_id=%s  tags=%v", ollama.HuggingFaceAPIURL, family, time.Since(t0).Round(time.Millisecond), info.ModelID, info.Tags)
		}

		// Detect system RAM for hardware check.
		systemRAM := ollama.GetSystemRAMGB()
		ux.Debugf("system RAM: %.1f GB", systemRAM)

		// Split into local and cloud model lists for separate ranking.
		var localModels, cloudModelsList []ollama.Model
		for _, m := range models {
			if m.Provider == "" || m.Provider == "ollama" {
				localModels = append(localModels, m)
			} else {
				cloudModelsList = append(cloudModelsList, m)
			}
		}

		rankedCloud := ollama.RankModels(cloudModelsList, 20, sweScores, nil, 0, "swe")
		rankedLocal := ollama.RankModels(localModels, 0, sweScores, hfInfoMap, systemRAM, "")

		ux.Debugf("ranking: %d cloud models (by SWE score), %d local models (by recommended)", len(rankedCloud), len(rankedLocal))
		ux.Debugf("note: scores are for full-precision models with agentic scaffolding")
		for _, r := range rankedCloud {
			if r.SWEScore > 0 {
				ux.Debugf("  cloud %s → %.1f%% [%s]", r.Name, r.SWEScore, r.ScoreSource)
			}
		}
		for _, r := range rankedLocal {
			if r.SWEScore > 0 {
				ux.Debugf("  local %s → %.1f%% [%s]", r.Name, r.SWEScore, r.ScoreSource)
			} else {
				ux.Debugf("  local %s → no rating data", r.Name)
			}
		}

		renderModels(rankedCloud, rankedLocal, cloudModelsList, localModels, sweScores, hfInfoMap, systemRAM)

		if ux.OutputFormat == "text" {
			fmt.Println(modGray.Render(fmt.Sprintf("%*s", 70, fmt.Sprintf("ollama %s", baseURL))))
			fmt.Println()
			if sweErr != nil {
				ux.Info("Scores from built-in estimates (SWE-bench fetch failed).")
			} else {
				ux.Info("Scores from SWE-bench Verified (full-model, not quantized).")
			}
			if len(rankedLocal) > 0 {
				ux.Info(fmt.Sprintf("Hardware: Q4 estimate vs %.0fGB RAM. --debug for details.", systemRAM))
				fmt.Println()
				snippet := ollama.FormatTOMLSnippet(rankedLocal)
				ux.Info(fmt.Sprintf("%d local models found. Add to ~/.config/devcell/devcell.toml:", len(rankedLocal)))
				fmt.Println()
				for _, line := range strings.Split(snippet, "\n") {
					fmt.Printf("  %s\n", line)
				}
			}
			fmt.Println()
		}

		return nil
	},
}

// buildAllRows converts a combined ranked model list to table rows.
// Columns: #, Model, Source, Rating, Speed, Size, Type, Score
// Source is the provider name for cloud models, or "local" for ollama models.
func buildAllRows(ranked []ollama.RankedModel, hfInfoMap map[string]ollama.HFModelInfo, systemRAM float64) [][]string {
	rows := make([][]string, 0, len(ranked))
	for _, r := range ranked {
		rating := modGray.Render("-")
		if r.SWEScore > 0 {
			label := fmt.Sprintf("~%.0f%%", r.SWEScore)
			if r.ScoreSource == "est" {
				label += " " + modGray.Render("est")
			}
			rating = label
		}
		family := ollama.ModelFamily(r.Name)
		taskLabel := "General"
		if info, ok := hfInfoMap[family]; ok {
			taskLabel = ollama.InferTaskLabel(info, r.Name)
		} else {
			taskLabel = ollama.InferTaskLabel(ollama.HFModelInfo{}, r.Name)
		}
		var source string
		if r.Provider == "" || r.Provider == "ollama" {
			source = modGreen.Render("local")
		} else {
			source = modGray.Render("cloud")
		}
		size := r.ParameterSize
		if size == "" {
			size = modGray.Render("-")
		}
		// For local models show RAM fit; cloud shows "-".
		if (r.Provider == "" || r.Provider == "ollama") && systemRAM > 0 {
			paramsB := ollama.ParseParamSize(r.ParameterSize)
			if paramsB > 0 {
				needed := ollama.EstimateRAMGB(paramsB)
				ratio := needed / systemRAM
				switch {
				case ratio > 1.00:
					size = modRed.Render(fmt.Sprintf("%.0fGB!", needed))
				case ratio > 0.90:
					size = modRed.Render(fmt.Sprintf("%.0fGB~", needed))
				case ratio > 0.75:
					size = ux.StyleWarning.Render(fmt.Sprintf("%.0fGB?", needed))
				default:
					size = modGreen.Render(fmt.Sprintf("%.0fGB", needed))
				}
			}
		}
		rows = append(rows, []string{
			fmt.Sprintf("%d", r.Rank),
			r.Name,
			source,
			rating,
			modGray.Render(fmt.Sprintf("%.0fT/m", r.SpeedTPM)),
			size,
			taskLabel,
			modGray.Render(fmt.Sprintf("%.1f", r.RecommendedScore)),
		})
	}
	return rows
}

// mergeAndRank combines cloud and local ranked lists, sorts by the given key,
// and assigns fresh sequential ranks. sortBy matches the values accepted by
// ollama.RankModels: "swe", "speed", "size", or "" / "recommended".
func mergeAndRank(cloud, local []ollama.RankedModel, sortBy string) []ollama.RankedModel {
	merged := make([]ollama.RankedModel, 0, len(cloud)+len(local))
	merged = append(merged, cloud...)
	merged = append(merged, local...)
	sort.Slice(merged, func(i, j int) bool {
		switch sortBy {
		case "swe":
			if merged[i].SWEScore != merged[j].SWEScore {
				return merged[i].SWEScore > merged[j].SWEScore
			}
		case "speed":
			if merged[i].SpeedTPM != merged[j].SpeedTPM {
				return merged[i].SpeedTPM > merged[j].SpeedTPM
			}
		case "size":
			si := ollama.ParseParamSize(merged[i].ParameterSize)
			sj := ollama.ParseParamSize(merged[j].ParameterSize)
			if si != sj {
				return si > sj
			}
		}
		// default / tiebreaker: recommended score
		if merged[i].RecommendedScore != merged[j].RecommendedScore {
			return merged[i].RecommendedScore > merged[j].RecommendedScore
		}
		return merged[i].Name < merged[j].Name
	})
	for i := range merged {
		merged[i].Rank = i + 1
	}
	return merged
}

// renderModels displays ranked cloud and local model lists in the current OutputFormat.
// In json/yaml mode, prose headers are suppressed.
// Extracted for testability without a live ollama daemon.
func renderModels(
	rankedCloud []ollama.RankedModel,
	rankedLocal []ollama.RankedModel,
	allCloud []ollama.Model,
	allLocal []ollama.Model,
	sweScores map[string]float64,
	hfInfoMap map[string]ollama.HFModelInfo,
	systemRAM float64,
) {
	if ux.OutputFormat != "text" {
		all := append(rankedCloud, rankedLocal...)
		entries := make([]ModelEntry, 0, len(all))
		for _, r := range all {
			family := ollama.ModelFamily(r.Name)
			taskLabel := "General"
			if info, ok := hfInfoMap[family]; ok {
				taskLabel = ollama.InferTaskLabel(info, r.Name)
			} else {
				taskLabel = ollama.InferTaskLabel(ollama.HFModelInfo{}, r.Name)
			}
			hw := ""
			if r.Provider == "" || r.Provider == "ollama" {
				if systemRAM > 0 {
					ok, needed := ollama.CheckHardware(r.ParameterSize, systemRAM)
					if needed > 0 {
						if ok {
							hw = fmt.Sprintf("OK (%.0fGB)", needed)
						} else {
							hw = fmt.Sprintf("%.0fGB needed", needed)
						}
					}
				}
			} else {
				hw = "cloud"
			}
			size := r.ParameterSize
			if size == "" {
				size = "-"
			}
			provider := r.Provider
			if provider == "" {
				provider = "ollama"
			}
			entries = append(entries, ModelEntry{
				Rank:             r.Rank,
				Name:             r.Name,
				Provider:         provider,
				SWEScore:         r.SWEScore,
				Size:             size,
				Type:             taskLabel,
				SpeedTPM:         r.SpeedTPM,
				Hardware:         hw,
				RecommendedScore: r.RecommendedScore,
			})
		}
		ux.PrintData(entries)
		return
	}

	// Text mode: single combined table ranked by recommended score.
	merged := mergeAndRank(rankedCloud, rankedLocal, "")
	if len(merged) > 0 {
		fmt.Println()
		fmt.Println(modBold.Render(" Available Models  (local + cloud, ranked by recommended score)"))
		fmt.Println()
		headers := []string{"#", "Model", "Source", "Rating", "Speed", "Size", "Type", "Score"}
		rows := buildAllRows(merged, hfInfoMap, systemRAM)
		ux.InteractiveTable(headers, rows, func(key ux.SortKey) [][]string {
			sortBy := ux.SortKeyString(key)
			reSortedCloud := ollama.RankModels(allCloud, 20, sweScores, nil, 0, sortBy)
			reSortedLocal := ollama.RankModels(allLocal, 0, sweScores, hfInfoMap, systemRAM, sortBy)
			reMerged := mergeAndRank(reSortedCloud, reSortedLocal, sortBy)
			return buildAllRows(reMerged, hfInfoMap, systemRAM)
		})
	}
}

func init() {
	modelsCmd.Flags().Bool("debug", false, "Show detailed detection and ranking logs")
	modelsCmd.Flags().String("source", "all", "Filter models by source: local, cloud, all")
}
