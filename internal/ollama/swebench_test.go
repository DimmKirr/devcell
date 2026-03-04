package ollama_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/DimmKirr/devcell/internal/ollama"
)

const testLeaderboardJSON = `{
	"leaderboards": [{
		"name": "Verified",
		"results": [
			{
				"name": "Agent1 + DeepSeek-R1",
				"resolved": 49.2,
				"os_model": true,
				"tags": ["Model: deepseek-r1", "Org: DeepSeek"]
			},
			{
				"name": "Agent2 + DeepSeek-R1",
				"resolved": 45.0,
				"os_model": true,
				"tags": ["Model: deepseek-r1"]
			},
			{
				"name": "Agent1 + Qwen 2.5 Coder",
				"resolved": 35.0,
				"os_model": true,
				"tags": ["Model: https://huggingface.co/Qwen/Qwen2.5-Coder-32B-Instruct"]
			},
			{
				"name": "Agent1 + CodeLlama 70B",
				"resolved": 20.0,
				"os_model": true,
				"tags": ["Model: codellama"]
			},
			{
				"name": "Claude 4.5 Opus + Agent",
				"resolved": 76.8,
				"os_model": false,
				"tags": ["Model: claude-4-5-opus"]
			},
			{
				"name": "No Model Tag Entry",
				"resolved": 30.0,
				"os_model": true,
				"tags": ["Org: Unknown"]
			}
		]
	}]
}`

func serveSWEBench(t *testing.T, payload string) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(payload))
	}))
	t.Cleanup(srv.Close)
	return srv
}

func TestFetchSWEBenchScores_ParsesVerifiedLeaderboard(t *testing.T) {
	srv := serveSWEBench(t, testLeaderboardJSON)

	scores, err := ollama.FetchSWEBenchScores(context.Background(), srv.URL)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Should take best score for deepseek-r1 (49.2, not 45.0).
	if score, ok := scores["deepseek-r1"]; !ok || score != 49.2 {
		t.Errorf("expected deepseek-r1=49.2, got %v (ok=%v)", score, ok)
	}

	if score, ok := scores["codellama"]; !ok || score != 20.0 {
		t.Errorf("expected codellama=20.0, got %v (ok=%v)", score, ok)
	}

	// claude-4-5-opus should be included (no os_model filter).
	if score, ok := scores["claude-4-5-opus"]; !ok || score != 76.8 {
		t.Errorf("expected claude-4-5-opus=76.8, got %v (ok=%v)", score, ok)
	}
}

func TestFetchSWEBenchScores_IncludesAllModelsRegardlessOfOSFlag(t *testing.T) {
	srv := serveSWEBench(t, testLeaderboardJSON)

	scores, err := ollama.FetchSWEBenchScores(context.Background(), srv.URL)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// os_model=false entry (claude) should be present.
	if _, ok := scores["claude-4-5-opus"]; !ok {
		t.Error("expected non-OS model to be included (no os_model filter)")
	}

	// os_model=true entry should also be present.
	if _, ok := scores["deepseek-r1"]; !ok {
		t.Error("expected OS model to be included")
	}
}

func TestFetchSWEBenchScores_ExtractsHFRepoFromURLTag(t *testing.T) {
	srv := serveSWEBench(t, testLeaderboardJSON)

	scores, err := ollama.FetchSWEBenchScores(context.Background(), srv.URL)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// The Qwen entry has tag "Model: https://huggingface.co/Qwen/Qwen2.5-Coder-32B-Instruct"
	// Should be stored under:
	//   1. full URL (lowercased)
	//   2. repo path: "qwen/qwen2.5-coder-32b-instruct"
	//   3. model name: "qwen2.5-coder-32b-instruct"

	if score, ok := scores["qwen/qwen2.5-coder-32b-instruct"]; !ok || score != 35.0 {
		t.Errorf("expected repo path key, got %v (ok=%v)", score, ok)
	}

	if score, ok := scores["qwen2.5-coder-32b-instruct"]; !ok || score != 35.0 {
		t.Errorf("expected model name key, got %v (ok=%v)", score, ok)
	}
}

func TestFetchSWEBenchScores_SkipsEntriesWithoutModelTag(t *testing.T) {
	srv := serveSWEBench(t, testLeaderboardJSON)

	scores, err := ollama.FetchSWEBenchScores(context.Background(), srv.URL)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// 5 entries have Model tags (deepseek-r1 x2, qwen HF URL, codellama, claude).
	// deepseek-r1 deduplicates to 1 key, qwen HF URL produces 3 keys, codellama 1, claude 1.
	// Total unique keys: deepseek-r1(1) + qwen(3: url, repo, name) + codellama(1) + claude(1) = 6.
	// The "No Model Tag Entry" should be skipped.
	if len(scores) != 6 {
		t.Errorf("expected 6 score keys, got %d: %v", len(scores), scores)
	}
}

func TestFetchSWEBenchScores_ReturnsErrorOnBadJSON(t *testing.T) {
	srv := serveSWEBench(t, "not json")

	_, err := ollama.FetchSWEBenchScores(context.Background(), srv.URL)
	if err == nil {
		t.Error("expected error for bad JSON")
	}
}

func TestFetchSWEBenchScores_ReturnsErrorOnHTTPFailure(t *testing.T) {
	_, err := ollama.FetchSWEBenchScores(context.Background(), "http://127.0.0.1:0")
	if err == nil {
		t.Error("expected error for unreachable server")
	}
}

func TestFetchSWEBenchScores_ReturnsErrorWhenNoVerifiedLeaderboard(t *testing.T) {
	data := `{"leaderboards": [{"name": "Other", "results": []}]}`
	srv := serveSWEBench(t, data)

	_, err := ollama.FetchSWEBenchScores(context.Background(), srv.URL)
	if err == nil {
		t.Error("expected error when Verified leaderboard not found")
	}
}

// --- FindSWEScore tests ---

func TestFindSWEScore_MatchByFullRepoID(t *testing.T) {
	scores := map[string]float64{
		"qwen/qwen2.5-coder-32b-instruct": 35.0,
	}

	score, ok := ollama.FindSWEScore(scores, "Qwen/Qwen2.5-Coder-32B-Instruct")
	if !ok || score != 35.0 {
		t.Errorf("expected 35.0, got %v (ok=%v)", score, ok)
	}
}

func TestFindSWEScore_MatchByModelNamePart(t *testing.T) {
	scores := map[string]float64{
		"qwen2.5-coder-32b-instruct": 35.0,
	}

	score, ok := ollama.FindSWEScore(scores, "Qwen/Qwen2.5-Coder-32B-Instruct")
	if !ok || score != 35.0 {
		t.Errorf("expected 35.0, got %v (ok=%v)", score, ok)
	}
}

func TestFindSWEScore_MatchBySubstringScan(t *testing.T) {
	scores := map[string]float64{
		"qwen2.5-coder-32b-instruct": 35.0,
	}

	// "qwen2.5-coder-32b" is a substring of the key.
	score, ok := ollama.FindSWEScore(scores, "qwen2.5-coder-32b")
	if !ok || score != 35.0 {
		t.Errorf("expected substring match, got %v (ok=%v)", score, ok)
	}
}

func TestFindSWEScore_NoMatch(t *testing.T) {
	scores := map[string]float64{
		"deepseek-r1": 49.2,
	}

	_, ok := ollama.FindSWEScore(scores, "totally-unknown/model")
	if ok {
		t.Error("expected no match for unknown model")
	}
}

func TestFindSWEScore_NilOrEmpty(t *testing.T) {
	_, ok := ollama.FindSWEScore(nil, "anything")
	if ok {
		t.Error("expected no match with nil scores")
	}

	_, ok = ollama.FindSWEScore(map[string]float64{"x": 1.0}, "")
	if ok {
		t.Error("expected no match with empty repoID")
	}
}

// --- MatchModelScore tests ---

func TestMatchModelScore_ExactFamilyMatch(t *testing.T) {
	scores := map[string]float64{
		"deepseek-r1": 49.2,
		"codellama":   20.0,
	}

	score, ok := ollama.MatchModelScore("deepseek-r1:32b", scores)
	if !ok || score != 49.2 {
		t.Errorf("expected 49.2, got %v (ok=%v)", score, ok)
	}

	score, ok = ollama.MatchModelScore("codellama:70b", scores)
	if !ok || score != 20.0 {
		t.Errorf("expected 20.0, got %v (ok=%v)", score, ok)
	}
}

func TestMatchModelScore_NoSizeSuffix(t *testing.T) {
	scores := map[string]float64{
		"deepseek-r1": 49.2,
	}

	score, ok := ollama.MatchModelScore("deepseek-r1:latest", scores)
	if !ok || score != 49.2 {
		t.Errorf("expected 49.2 for :latest suffix, got %v (ok=%v)", score, ok)
	}
}

func TestMatchModelScore_CaseInsensitive(t *testing.T) {
	scores := map[string]float64{
		"DeepSeek-R1": 49.2,
	}

	score, ok := ollama.MatchModelScore("deepseek-r1:32b", scores)
	if !ok || score != 49.2 {
		t.Errorf("expected case-insensitive match, got %v (ok=%v)", score, ok)
	}
}

func TestMatchModelScore_NoMatch(t *testing.T) {
	scores := map[string]float64{
		"deepseek-r1": 49.2,
	}

	_, ok := ollama.MatchModelScore("unknown-model:latest", scores)
	if ok {
		t.Error("expected no match for unknown model")
	}
}

func TestMatchModelScore_NilScores(t *testing.T) {
	_, ok := ollama.MatchModelScore("deepseek-r1:32b", nil)
	if ok {
		t.Error("expected no match with nil scores")
	}
}
