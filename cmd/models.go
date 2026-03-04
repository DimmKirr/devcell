package main

import (
	"context"
	"fmt"

	"github.com/DimmKirr/devcell/internal/ollama"
	"github.com/pterm/pterm"
	"github.com/spf13/cobra"
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
		if debug {
			pterm.EnableDebugMessages()
		}
		ctx := context.Background()
		baseURL := ollama.DefaultBaseURL

		if debug {
			pterm.Debug.Println("Checking ollama at " + baseURL)
		}

		if !ollama.Detect(ctx, baseURL) {
			pterm.Warning.Println("Ollama not detected at " + baseURL)
			pterm.Info.Println("Install ollama: https://ollama.com/download")
			return nil
		}

		if debug {
			pterm.Debug.Println("Ollama reachable, fetching model list via SDK (GET /api/tags)")
		}

		models, err := ollama.FetchModels(ctx, baseURL)
		if err != nil {
			return fmt.Errorf("fetch models: %w", err)
		}

		if debug {
			pterm.Debug.Printfln("Fetched %d models from ollama", len(models))
			for _, m := range models {
				pterm.Debug.Printfln("  %s (size=%s, family=%s)", m.Name, m.ParameterSize, m.Family)
			}
		}

		if len(models) == 0 {
			pterm.Warning.Println("Ollama is running but no models installed.")
			pterm.Info.Println("Pull a model: ollama pull deepseek-r1:32b")
			return nil
		}

		// Fetch live SWE-bench scores (falls back to hardcoded on failure).
		var sweScores map[string]float64
		if debug {
			pterm.Debug.Printfln("Fetching SWE-bench Verified leaderboard from %s", ollama.SWEBenchURL)
		}
		sweScores, sweErr := ollama.FetchSWEBenchScores(ctx, ollama.SWEBenchURL)
		if sweErr != nil {
			if debug {
				pterm.Debug.Printfln("SWE-bench fetch failed (using fallback ratings): %v", sweErr)
			}
		} else if debug {
			pterm.Debug.Printfln("Fetched %d open-source model scores from SWE-bench Verified", len(sweScores))
			for model, score := range sweScores {
				pterm.Debug.Printfln("  %s → %.1f%%", model, score)
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
					pterm.Debug.Printfln("HuggingFace lookup failed for %s: %v", family, hfErr)
				}
				continue
			}
			hfInfoMap[family] = info
			if debug {
				pterm.Debug.Printfln("HuggingFace: %s → %s (tags: %v)", family, info.ModelID, info.Tags)
			}
		}

		ranked := ollama.RankModels(models, 10, sweScores, hfInfoMap)

		if debug {
			pterm.Debug.Println("Ranking models (live SWE-bench scores where available, fallback estimates otherwise)")
			pterm.Debug.Println("Note: SWE-bench scores are for full-precision models with agentic scaffolding.")
			pterm.Debug.Println("      Quantized ollama variants will score lower in practice.")
			pterm.Debug.Println("Sources: https://www.swebench.com/ | https://epoch.ai/benchmarks/swe-bench-verified")
			for _, r := range ranked {
				if r.SWEScore > 0 {
					pterm.Debug.Printfln("  %s → %.1f%% [%s]", r.Name, r.SWEScore, r.ScoreSource)
				} else {
					pterm.Debug.Printfln("  %s → no rating data", r.Name)
				}
			}
		}

		// Detect system RAM for hardware check.
		systemRAM := ollama.GetSystemRAMGB()
		if debug {
			pterm.Debug.Printfln("System RAM: %.1f GB", systemRAM)
		}

		pterm.DefaultSection.Println("Local Models (ranked by SWE-Bench score)")

		tableData := pterm.TableData{
			{"#", "Model", "Rating", "Size", "Type", "Hardware"},
		}
		for _, r := range ranked {
			score := pterm.Gray("-")
			if r.SWEScore > 0 {
				label := fmt.Sprintf("~%.0f%%", r.SWEScore)
				if r.ScoreSource != "" {
					label += " " + pterm.Gray(r.ScoreSource)
				}
				score = label
			}
			size := r.ParameterSize
			if size == "" {
				size = "-"
			}

			// Task type from HuggingFace.
			family := ollama.ModelFamily(r.Name)
			taskLabel := "General"
			if info, ok := hfInfoMap[family]; ok {
				taskLabel = ollama.InferTaskLabel(info, r.Name)
			} else {
				taskLabel = ollama.InferTaskLabel(ollama.HFModelInfo{}, r.Name)
			}

			// Hardware check.
			hwLabel := pterm.Gray("-")
			if systemRAM > 0 {
				ok, needed := ollama.CheckHardware(r.ParameterSize, systemRAM)
				if needed > 0 {
					if ok {
						hwLabel = pterm.Green(fmt.Sprintf("OK (%.0fGB)", needed))
					} else {
						hwLabel = pterm.Red(fmt.Sprintf("%.0fGB needed", needed))
					}
				}
			}

			tableData = append(tableData, []string{
				fmt.Sprintf("%d", r.Rank),
				r.Name,
				score,
				size,
				taskLabel,
				hwLabel,
			})
		}

		pterm.DefaultTable.WithHasHeader().WithBoxed().WithData(tableData).Render()
		pterm.DefaultBasicText.WithStyle(pterm.NewStyle(pterm.FgGray)).
			Printfln("%*s", 70, fmt.Sprintf("ollama %s", baseURL))

		pterm.Println()
		if sweErr != nil {
			pterm.Info.Printfln("Scores from built-in estimates (SWE-bench fetch failed).")
		} else {
			pterm.Info.Printfln("Scores from SWE-bench Verified (full-model, not quantized).")
		}
		pterm.Info.Printfln("Hardware: Q4 estimate vs %.0fGB RAM. --debug for details.", systemRAM)
		pterm.Println()

		snippet := ollama.FormatTOMLSnippet(ranked)
		pterm.Info.Printfln("%d models found. Add to ~/.config/devcell/devcell.toml:\n", len(ranked))
		for _, line := range splitLines(snippet) {
			fmt.Printf("  %s\n", line)
		}
		pterm.Println()

		return nil
	},
}

func init() {
	modelsCmd.Flags().Bool("debug", false, "Show detailed detection and ranking logs")
}

func splitLines(s string) []string {
	var lines []string
	start := 0
	for i := 0; i < len(s); i++ {
		if s[i] == '\n' {
			lines = append(lines, s[start:i])
			start = i + 1
		}
	}
	if start < len(s) {
		lines = append(lines, s[start:])
	}
	return lines
}
