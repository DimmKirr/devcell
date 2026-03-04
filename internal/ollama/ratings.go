package ollama

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
	// DeepSeek-R1 distilled (NOT on SWE-bench Verified)
	// Full R1 671B: LiveCodeBench 65.9%, Aider Polyglot 57-71%, Codeforces 2029
	// Distilled: LiveCodeBench 70B=65.2%, 32B=62.1%, 14B=59.1%, 7B=49.1%, 1.5B=33.8%
	// Codeforces: 70B=1633, 32B=1691, 14B=1481, 7B=1189, 1.5B=954
	// Q4 quantization discount applied (~15-20%)
	"deepseek-r1:70b":  32.0, // LiveCodeBench 65.2%, Codeforces 1633
	"deepseek-r1:32b":  30.0, // LiveCodeBench 62.1%, Codeforces 1691 (close to 70B!)
	"deepseek-r1:14b":  22.0, // LiveCodeBench 59.1%, Codeforces 1481
	"deepseek-r1:8b":   12.0, // LiveCodeBench ~49% (7B=49.1, 8B=Llama-8B similar)
	"deepseek-r1:7b":   11.0, // LiveCodeBench 49.1%, Codeforces 1189
	"deepseek-r1:1.5b": 3.0,  // LiveCodeBench 33.8%, Codeforces 954

	// Qwen 3 (base chat model, not code-specialized)
	// Aider Polyglot: 235B=59.6%, 32B=40.0%
	// Qwen3-Coder-480B/A35B scores 55.4% SWE-bench (mini-agent), 69.6% (OpenHands)
	"qwen3:235b": 40.0, // Aider 59.6%, MoE A22B active params
	"qwen3:32b":  26.0, // Aider 40.0%
	"qwen3:30b":  25.0, // MoE variant (A3B), similar to 32B
	"qwen3:8b":   14.0, // extrapolated from 32B scaling
	"qwen3:4b":   7.0,
	"qwen3:1.7b": 4.0,
	"qwen3:0.6b": 1.0,

	// Qwen 2.5 Coder (code-specific fine-tune)
	// SWE-bench: 47% best (Skywork+TTS), 38% (Skywork), 9% (mini-agent)
	// Aider Edit: 32B=72.9%, 14B=61.7%, 7B=57.9%
	// Aider Polyglot: 32B=16.4% (polyglot is much harder than edit benchmark)
	"qwen2.5-coder:32b":  22.0, // strong at code editing; weaker at multi-step
	"qwen2.5-coder:14b":  15.0, // Aider Edit 61.7%
	"qwen2.5-coder:7b":   10.0, // Aider Edit 57.9%
	"qwen2.5-coder:3b":   5.0,
	"qwen2.5-coder:1.5b": 3.0,

	// CodeLlama (2023-era models, significantly weaker than modern alternatives)
	// HumanEval: 70B=67%, 34B=54%, 13B~47%, 7B~33%
	// Not on Aider Polyglot or SWE-bench; scaled relative to modern models
	"codellama:70b": 10.0, // HumanEval 67% but weak at multi-step tasks
	"codellama:34b": 7.0,  // HumanEval 54%
	"codellama:13b": 4.0,  // HumanEval ~47%
	"codellama:7b":  2.0,  // HumanEval ~33%

	// DeepSeek Coder V2 (2024, strong for its era)
	// V2-16B: HumanEval 81.1%
	// V1-33B: HumanEval ~70%
	"deepseek-coder-v2:16b": 18.0, // HumanEval 81.1%, MoE architecture
	"deepseek-coder:33b":    12.0,
	"deepseek-coder:6.7b":   7.0,

	// Devstral (Mistral code model)
	// SWE-bench mini-agent: small=56.4%, full=53.8%
	"devstral:latest": 24.0,

	// Llama 3.1 (general purpose, not code-specialized)
	// Aider Edit: 70B=58.6%, 8B=37.6%
	"llama3.1:70b": 16.0,
	"llama3.1:8b":  5.0,

	// Phi-4 (Microsoft, compact reasoning model)
	"phi4:14b": 14.0,

	// Gemma 3 (Google, Aider Polyglot: 27B=4.9%)
	"gemma3:27b": 4.0,
	"gemma3:12b": 2.0,

	// Codestral (Mistral code model, Aider Polyglot: 11.1%)
	"codestral:latest": 8.0,

	// Mistral general
	"mistral:7b": 3.0,

	// Llama 4 (Meta, Aider Polyglot: Maverick=15.6%)
	"llama4:maverick": 12.0,
	"llama4:scout":    6.0,
}
