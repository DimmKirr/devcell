package ollama

import "strings"

// sweBenchRatings maps ollama model names to estimated coding capability scores.
//
// These are NOT direct SWE-bench Verified scores for the quantized variants.
// SWE-bench Verified only tests full-precision API models with agentic scaffolding.
//
// Scores here are estimates derived from multiple benchmark sources and scaled
// to be roughly comparable to SWE-bench Verified (0-100% scale).
// Quantized Q4_K_M variants are discounted ~15-20% from full-precision scores.
//
// Sources used for calibration:
//   - SWE-bench Verified: https://www.swebench.com/ (180 entries, All Agents)
//   - Aider Polyglot:     https://aider.chat/docs/leaderboards/
//   - LiveCodeBench:      https://livecodebench.github.io/
//   - DeepSeek-R1 paper:  https://arxiv.org/abs/2501.12948 (Table 5)
//   - Qwen2.5-Coder:      https://qwenlm.github.io/blog/qwen2.5-coder-family/
//   - CodeLlama paper:    https://arxiv.org/abs/2308.12950
//   - DeepSeek-Coder-V2:  https://arxiv.org/abs/2406.11931
var sweBenchRatings = map[string]float64{
	// Qwen 3 Coder Next (MoE 80B/A3B, Feb 2026)
	// SWE-bench Verified: 70.6% (SWE-Agent), Aider: 66.2%
	// Near Sonnet 4.5 level despite only 3B active params
	"qwen3-coder-next:latest": 58.0,

	// Qwen 3 Coder (MoE 30B/A3B, agentic coding model)
	// SWE-bench Verified: 67-69.6% (OpenHands), Aider: ~60%
	"qwen3-coder:30b": 55.0,

	// GLM-4.7 Flash (THUDM, 30B)
	// SWE-bench Verified: 74.2% (full-precision 358B)
	// Flash variant is distilled, Q4 discount applied
	"glm-4.7-flash:latest": 35.0,

	// DeepSeek-R1 distilled (NOT on SWE-bench Verified)
	// Full R1 671B: LiveCodeBench 65.9%, Aider Polyglot 57-71%
	// Distilled: LiveCodeBench 70B=65.2%, 32B=62.1%, 14B=59.1%
	// Q4 quantization discount applied (~15-20%)
	"deepseek-r1:70b":  32.0,
	"deepseek-r1:32b":  30.0,
	"deepseek-r1:14b":  22.0,
	"deepseek-r1:8b":   12.0,
	"deepseek-r1:7b":   11.0,
	"deepseek-r1:1.5b": 3.0,

	// Devstral (Mistral code models)
	// SWE-bench mini-agent: small=56.4%, full=53.8%
	"devstral:latest":       24.0,
	"devstral-small:latest": 45.0, // devstral-small-2, SWE-bench 56.4%

	// Qwen 3.5 (March 2026, multimodal, 262K context)
	// Beats qwen3-30B on reasoning despite 9B size
	"qwen3.5:9b":  20.0,
	"qwen3.5:32b": 30.0,

	// Qwen 3 (base chat model, not code-specialized)
	// Aider Polyglot: 235B=59.6%, 32B=40.0%
	"qwen3:235b": 40.0,
	"qwen3:32b":  26.0,
	"qwen3:30b":  25.0,
	"qwen3:8b":   14.0,
	"qwen3:4b":   7.0,
	"qwen3:1.7b": 4.0,
	"qwen3:0.6b": 1.0,

	// Qwen 2.5 Coder (code-specific fine-tune)
	// SWE-bench: 47% best (Skywork+TTS), Aider Edit: 32B=72.9%
	"qwen2.5-coder:32b":  22.0,
	"qwen2.5-coder:14b":  15.0,
	"qwen2.5-coder:7b":   10.0,
	"qwen2.5-coder:3b":   5.0,
	"qwen2.5-coder:1.5b": 3.0,

	// CodeLlama (2023-era, significantly weaker than modern alternatives)
	"codellama:70b": 10.0,
	"codellama:34b": 7.0,
	"codellama:13b": 4.0,
	"codellama:7b":  2.0,

	// DeepSeek Coder V2 (2024)
	"deepseek-coder-v2:16b": 18.0,
	"deepseek-coder:33b":    12.0,
	"deepseek-coder:6.7b":   7.0,

	// Llama 3.1 (general purpose)
	"llama3.1:70b": 16.0,
	"llama3.1:8b":  5.0,

	// Phi-4 (Microsoft, compact reasoning)
	"phi4:14b": 14.0,

	// Gemma 3 (Google)
	"gemma3:27b": 4.0,
	"gemma3:12b": 2.0,

	// Codestral (Mistral code model)
	"codestral:latest": 8.0,

	// Mistral general
	"mistral:7b": 3.0,

	// Llama 4 (Meta)
	"llama4:maverick": 12.0,
	"llama4:scout":    6.0,
}

// cloudModelRatings maps normalized cloud model IDs (output of NormalizeCloudID)
// to SWE-bench Verified percentage scores.
//
// Keys are matched with prefix logic so "claude-haiku-4-5-20251001" matches
// "claude-haiku-4-5". Use the shortest unambiguous prefix as the key.
//
// Sources:
//   - SWE-bench Verified leaderboard: https://www.swebench.com/
//   - Anthropic model cards and benchmark announcements
//   - OpenAI o-series and GPT benchmark disclosures
//   - Google Gemini technical reports
var cloudModelRatings = map[string]float64{
	// Anthropic — SWE-bench Verified scores
	"claude-opus-4-5":   76.8, // Claude Opus 4.5 — confirmed leaderboard entry
	"claude-opus-4-6":   76.8, // Claude Opus 4.6 — same family, conservative estimate
	"claude-opus-4":     72.0, // Claude Opus 4
	"claude-sonnet-4-5": 72.7, // Claude Sonnet 4.5
	"claude-sonnet-4":   49.0, // Claude Sonnet 4
	"claude-haiku-4-5":  43.0, // Claude Haiku 4.5
	"claude-3-7-sonnet": 62.3, // Claude 3.7 Sonnet
	"claude-3-5-sonnet": 49.0, // Claude 3.5 Sonnet (2024-10)
	"claude-3-5-haiku":  40.6, // Claude 3.5 Haiku
	"claude-3-opus":     11.1, // Claude 3 Opus (older baseline)

	// OpenAI — SWE-bench Verified scores
	"o3":       71.7, // o3 (high-compute)
	"o4-mini":  68.1, // o4-mini
	"o3-mini":  49.3, // o3-mini
	"gpt-4-1":  54.6, // GPT-4.1
	"o1":       48.9, // o1
	"gpt-4o":   33.2, // GPT-4o
	"gpt-4-1-mini": 34.6, // GPT-4.1 mini
	"o1-mini":  16.7, // o1-mini
	"gpt-4o-mini": 23.7, // GPT-4o mini

	// Google — SWE-bench Verified scores
	"gemini-2-5-pro":   63.8, // Gemini 2.5 Pro
	"gemini-2-0-flash": 41.3, // Gemini 2.0 Flash
	"gemini-1-5-pro":   26.7, // Gemini 1.5 Pro
	"gemini-1-5-flash": 18.6, // Gemini 1.5 Flash
}

// lookupCloudRating finds a score for a normalized cloud model ID.
// Tries exact match first, then prefix match (longest wins).
func lookupCloudRating(normalized string) (float64, bool) {
	if s, ok := cloudModelRatings[normalized]; ok {
		return s, true
	}
	var bestScore float64
	var bestLen int
	for key, score := range cloudModelRatings {
		if strings.HasPrefix(normalized, key) && len(key) > bestLen {
			bestScore = score
			bestLen = len(key)
		}
	}
	if bestLen > 0 {
		return bestScore, true
	}
	return 0, false
}

// EstimateCloudSpeedTPM estimates cloud model speed (tokens/min) from
// completion price per token (USD). Cheaper models are typically faster.
func EstimateCloudSpeedTPM(pricePerToken float64) float64 {
	switch {
	case pricePerToken < 0.000001: // < $1/1M tokens
		return 18000
	case pricePerToken < 0.000005: // < $5/1M
		return 9000
	case pricePerToken < 0.000015: // < $15/1M
		return 5400
	case pricePerToken < 0.000050: // < $50/1M
		return 2400
	default:
		return 1200
	}
}

// EstimateLocalSpeedTPM estimates local model speed (tokens/min).
//
// When bandwidthGBs > 0 (Apple Silicon detected), uses the physics formula:
//
//	speed = bandwidth / model_weight_bytes_per_token
//
// Q4_K_M quantization ≈ 0.5625 bytes/param (4.5 bits/param).
// Capped at 9000 T/min (150 tok/s) — the compute-bound ceiling for tiny models.
//
// When bandwidthGBs == 0, falls back to generic tier estimates for
// average consumer GPU hardware.
func EstimateLocalSpeedTPM(paramsB, bandwidthGBs float64) float64 {
	if bandwidthGBs > 0 && paramsB > 0 {
		const (
			q4BytesPerParam  = 0.5625 // Q4_K_M ≈ 4.5 bits/param = 0.5625 bytes/param
			bandwidthEff     = 0.78   // llama.cpp Metal achieves ~75-80% of theoretical bandwidth
			maxTokPerSec     = 200    // compute-bound ceiling for sub-3B models on Apple Silicon
		)
		tokPerSec := (bandwidthGBs * bandwidthEff) / (paramsB * q4BytesPerParam)
		if tokPerSec > maxTokPerSec {
			tokPerSec = maxTokPerSec
		}
		return tokPerSec * 60
	}
	// Generic tier fallback (average consumer GPU).
	switch {
	case paramsB <= 3:
		return 9000
	case paramsB <= 8:
		return 4800
	case paramsB <= 14:
		return 2700
	case paramsB <= 32:
		return 1500
	case paramsB <= 70:
		return 720
	default:
		return 180
	}
}
