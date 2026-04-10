package main

import (
	"context"
	"fmt"
	"log/slog"
	"strings"

	"github.com/DimmKirr/devcell/internal/ollama"
	"github.com/DimmKirr/devcell/internal/ux"
	"github.com/spf13/cobra"
)

// ModelEntry is the typed representation of a ranked model for JSON/YAML output.
type ModelEntry struct {
	Rank     int     `json:"rank"      yaml:"rank"`
	Name     string  `json:"name"      yaml:"name"`
	SWEScore float64 `json:"swe_score" yaml:"swe_score"`
	Size     string  `json:"size"      yaml:"size"`
	Type     string  `json:"type"      yaml:"type"`
	Hardware string  `json:"hardware"  yaml:"hardware"`
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

    cell models
    cell models --debug`,
	RunE: func(cmd *cobra.Command, args []string) error {
		debug, _ := cmd.Flags().GetBool("debug")
		log := slog.Default()
		ctx := context.Background()
		baseURL := ollama.DefaultBaseURL

		if debug {
			log.Debug("Checking ollama at " + baseURL)
		}

		if !ollama.Detect(ctx, baseURL) {
			ux.Warn("Ollama not detected at " + baseURL)
			ux.Info("Install ollama: https://ollama.com/download")
			return nil
		}

		if debug {
			log.Debug("Ollama reachable, fetching model list via SDK (GET /api/tags)")
		}

		models, err := ollama.FetchModels(ctx, baseURL)
		if err != nil {
			return fmt.Errorf("fetch models: %w", err)
		}

		if debug {
			log.Debug(fmt.Sprintf("Fetched %d models from ollama", len(models)))
			for _, m := range models {
				log.Debug(fmt.Sprintf("  %s (size=%s, family=%s)", m.Name, m.ParameterSize, m.Family))
			}
		}

		if len(models) == 0 {
			ux.Warn("Ollama is running but no models installed.")
			ux.Info("Pull a model: ollama pull deepseek-r1:32b")
			return nil
		}

		// Fetch live SWE-bench scores (falls back to hardcoded on failure).
		var sweScores map[string]float64
		if debug {
			log.Debug(fmt.Sprintf("Fetching SWE-bench Verified leaderboard from %s", ollama.SWEBenchURL))
		}
		sweScores, sweErr := ollama.FetchSWEBenchScores(ctx, ollama.SWEBenchURL)
		if sweErr != nil {
			if debug {
				log.Debug(fmt.Sprintf("SWE-bench fetch failed (using fallback ratings): %v", sweErr))
			}
		} else if debug {
			log.Debug(fmt.Sprintf("Fetched %d open-source model scores from SWE-bench Verified", len(sweScores)))
			for model, score := range sweScores {
				log.Debug(fmt.Sprintf("  %s → %.1f%%", model, score))
			}
		}

		// Fetch HuggingFace model info (best effort, per model) BEFORE ranking,
		// so RankModels can use HF repo IDs for SWE-bench score matching.
		hfInfoMap := make(map[string]ollama.HFModelInfo)
		for _, m := range models {
			family := ollama.ModelFamily(m.Name)
			if _, done := hfInfoMap[family]; done {
				continue
			}
			info, hfErr := ollama.FetchHFModelInfo(ctx, ollama.HuggingFaceAPIURL, family)
			if hfErr != nil {
				if debug {
					log.Debug(fmt.Sprintf("HuggingFace lookup failed for %s: %v", family, hfErr))
				}
				continue
			}
			hfInfoMap[family] = info
			if debug {
				log.Debug(fmt.Sprintf("HuggingFace: %s → %s (tags: %v)", family, info.ModelID, info.Tags))
			}
		}

		ranked := ollama.RankModels(models, 10, sweScores, hfInfoMap)

		if debug {
			log.Debug("Ranking models (live SWE-bench scores where available, fallback estimates otherwise)")
			log.Debug("Note: SWE-bench scores are for full-precision models with agentic scaffolding.")
			log.Debug("      Quantized ollama variants will score lower in practice.")
			log.Debug("Sources: https://www.swebench.com/ | https://epoch.ai/benchmarks/swe-bench-verified")
			for _, r := range ranked {
				if r.SWEScore > 0 {
					log.Debug(fmt.Sprintf("  %s → %.1f%% [%s]", r.Name, r.SWEScore, r.ScoreSource))
				} else {
					log.Debug(fmt.Sprintf("  %s → no rating data", r.Name))
				}
			}
		}

		// Detect system RAM for hardware check.
		systemRAM := ollama.GetSystemRAMGB()
		if debug {
			log.Debug(fmt.Sprintf("System RAM: %.1f GB", systemRAM))
		}

		renderModels(ranked, hfInfoMap, systemRAM)

		if ux.OutputFormat == "text" {
			fmt.Println(modGray.Render(fmt.Sprintf("%*s", 70, fmt.Sprintf("ollama %s", baseURL))))
			fmt.Println()
			if sweErr != nil {
				ux.Info("Scores from built-in estimates (SWE-bench fetch failed).")
			} else {
				ux.Info("Scores from SWE-bench Verified (full-model, not quantized).")
			}
			ux.Info(fmt.Sprintf("Hardware: Q4 estimate vs %.0fGB RAM. --debug for details.", systemRAM))
			fmt.Println()

			snippet := ollama.FormatTOMLSnippet(ranked)
			ux.Info(fmt.Sprintf("%d models found. Add to ~/.config/devcell/devcell.toml:", len(ranked)))
			fmt.Println()
			for _, line := range strings.Split(snippet, "\n") {
				fmt.Printf("  %s\n", line)
			}
			fmt.Println()
		}

		return nil
	},
}

// renderModels displays the ranked model list in the current OutputFormat.
// In json/yaml mode, prose (header, TOML snippet, footer) is suppressed.
// Extracted for testability without a live ollama daemon.
func renderModels(ranked []ollama.RankedModel, hfInfoMap map[string]ollama.HFModelInfo, systemRAM float64) {
	if ux.OutputFormat != "text" {
		entries := make([]ModelEntry, 0, len(ranked))
		for _, r := range ranked {
			family := ollama.ModelFamily(r.Name)
			taskLabel := "General"
			if info, ok := hfInfoMap[family]; ok {
				taskLabel = ollama.InferTaskLabel(info, r.Name)
			} else {
				taskLabel = ollama.InferTaskLabel(ollama.HFModelInfo{}, r.Name)
			}
			hw := ""
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
			size := r.ParameterSize
			if size == "" {
				size = "-"
			}
			entries = append(entries, ModelEntry{
				Rank:     r.Rank,
				Name:     r.Name,
				SWEScore: r.SWEScore,
				Size:     size,
				Type:     taskLabel,
				Hardware: hw,
			})
		}
		ux.PrintData(entries)
		return
	}

	// Text mode: prose header + styled table.
	fmt.Println()
	fmt.Println(modBold.Render(" Local Models (ranked by SWE-Bench score)"))
	fmt.Println()

	headers := []string{"#", "Model", "Rating", "Size", "Type", "Hardware"}
	rows := make([][]string, 0, len(ranked))
	for _, r := range ranked {
		score := modGray.Render("-")
		if r.SWEScore > 0 {
			label := fmt.Sprintf("~%.0f%%", r.SWEScore)
			if r.ScoreSource != "" {
				label += " " + modGray.Render(r.ScoreSource)
			}
			score = label
		}
		size := r.ParameterSize
		if size == "" {
			size = "-"
		}
		family := ollama.ModelFamily(r.Name)
		taskLabel := "General"
		if info, ok := hfInfoMap[family]; ok {
			taskLabel = ollama.InferTaskLabel(info, r.Name)
		} else {
			taskLabel = ollama.InferTaskLabel(ollama.HFModelInfo{}, r.Name)
		}
		hwLabel := modGray.Render("-")
		if systemRAM > 0 {
			ok, needed := ollama.CheckHardware(r.ParameterSize, systemRAM)
			if needed > 0 {
				if ok {
					hwLabel = modGreen.Render(fmt.Sprintf("OK (%.0fGB)", needed))
				} else {
					hwLabel = modRed.Render(fmt.Sprintf("%.0fGB needed", needed))
				}
			}
		}
		rows = append(rows, []string{
			fmt.Sprintf("%d", r.Rank),
			r.Name,
			score,
			size,
			taskLabel,
			hwLabel,
		})
	}
	ux.PrintTable(headers, rows)
}

func init() {
	modelsCmd.Flags().Bool("debug", false, "Show detailed detection and ranking logs")
}
