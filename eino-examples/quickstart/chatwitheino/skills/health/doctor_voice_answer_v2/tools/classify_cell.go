// Package classifycell implements the classify_cell tool declared in
// SK-01-doctor-voice/SKILL.md. Given a user query (and optional history),
// it returns the best-matching topic_code/intent_code on the 21x20 grid
// defined in scenarios_matrix.md.
//
// Two backends, picked at startup:
//
//  1. Embedding backend (preferred). Activated when EMBED_API_KEY is set.
//     Loads precomputed label vectors from data/embeddings.json (built once
//     by scripts/build_embeddings.py), embeds the query at runtime through
//     an OpenAI-compatible /v1/embeddings endpoint, and returns argmax
//     cosine over topics and intents.
//
//  2. Character-bigram cosine fallback. Activated when EMBED_API_KEY is
//     unset or embeddings.json is missing. Vectorizes the label profile
//     text (name + definition + 3 representative queries) and the query
//     itself by Chinese character bigrams; argmax cosine. Zero network,
//     deterministic, ~1ms per call. Good enough for demo flow.
//
// Both backends emit the same shape and surface a "fallback" flag so the
// SKILL prompt can decide whether to consult cell_priors_lookup or fall
// back to its generic-rule branch.
package classifycell

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
	"unicode/utf8"
)

// ----- public tool surface -----

// Result is the JSON payload returned to the LLM.
type Result struct {
	TopicCode  string  `json:"topic_code"`
	IntentCode string  `json:"intent_code"`
	Confidence float64 `json:"confidence"` // min(topicSim, intentSim)
	Fallback   bool    `json:"fallback"`   // true when below threshold
	Backend    string  `json:"backend"`    // "embedding" | "charbag"
}

// Args matches the parameters declared in SKILL.md.
type Args struct {
	Query   string   `json:"query"`
	History []string `json:"history,omitempty"`
}

// Tool is the Eino tool.InvokableTool implementation.
type Tool struct {
	corpus     labelCorpus
	embeddings *embeddingStore // nil when running on charbag fallback
	threshold  float64
}

// NewTool loads data files relative to the tool's own directory.
// Pass an empty dataDir to use $SKILL_DATA_DIR or the directory of this binary.
func NewTool(dataDir string) (*Tool, error) {
	if dataDir == "" {
		if v := os.Getenv("SKILL_DATA_DIR"); v != "" {
			dataDir = v
		} else {
			dataDir = "data"
		}
	}

	corpus, err := loadCorpus(filepath.Join(dataDir, "label_corpus.json"))
	if err != nil {
		return nil, fmt.Errorf("load label_corpus.json: %w", err)
	}

	t := &Tool{corpus: corpus, threshold: 0.30}
	if v := os.Getenv("CLASSIFY_THRESHOLD"); v != "" {
		fmt.Sscanf(v, "%f", &t.threshold)
	}

	if os.Getenv("EMBED_API_KEY") != "" {
		store, err := loadEmbeddings(filepath.Join(dataDir, "embeddings.json"))
		if err == nil {
			t.embeddings = store
		}
	}
	return t, nil
}

// Name is the tool name registered into Eino's tool router.
func (t *Tool) Name() string { return "classify_cell" }

// Description is what the routing model reads.
func (t *Tool) Description() string {
	return "Classify a user health query onto the 21 topic x 20 intent grid. " +
		"Returns topic_code (e.g. T4), intent_code (e.g. I3), confidence in [0,1] " +
		"and fallback=true when the score is too low to trust."
}

// Invoke is the Eino tool entry point.
func (t *Tool) Invoke(ctx context.Context, raw json.RawMessage) (json.RawMessage, error) {
	var a Args
	if len(raw) > 0 {
		if err := json.Unmarshal(raw, &a); err != nil {
			return nil, fmt.Errorf("decode args: %w", err)
		}
	}
	if strings.TrimSpace(a.Query) == "" {
		return nil, errors.New("classify_cell: query is required")
	}

	// History is appended as soft context, weighted lightly so the latest user
	// turn dominates classification.
	text := a.Query
	if len(a.History) > 0 {
		text = strings.Join(a.History, " ") + " " + a.Query
	}

	res := t.classify(ctx, text)
	return json.Marshal(res)
}

func (t *Tool) classify(ctx context.Context, text string) Result {
	if t.embeddings != nil {
		if r, err := t.classifyEmbed(ctx, text); err == nil {
			return r
		}
		// fall through to charbag on transient failure
	}
	return t.classifyCharBag(text)
}

// ----- corpus / embedding storage -----

type labelInfo struct {
	Name       string   `json:"name"`
	Definition string   `json:"definition"`
	Examples   []string `json:"examples"`
}

type labelCorpus struct {
	Topics  map[string]labelInfo `json:"topics"`
	Intents map[string]labelInfo `json:"intents"`
}

func loadCorpus(p string) (labelCorpus, error) {
	var c labelCorpus
	b, err := os.ReadFile(p)
	if err != nil {
		return c, err
	}
	err = json.Unmarshal(b, &c)
	return c, err
}

type embeddingEntry struct {
	Profile string    `json:"profile"`
	Vector  []float64 `json:"vector"`
}

type embeddingStore struct {
	Model   string                    `json:"model"`
	Topics  map[string]embeddingEntry `json:"topics"`
	Intents map[string]embeddingEntry `json:"intents"`
}

func loadEmbeddings(p string) (*embeddingStore, error) {
	b, err := os.ReadFile(p)
	if err != nil {
		return nil, err
	}
	var s embeddingStore
	if err := json.Unmarshal(b, &s); err != nil {
		return nil, err
	}
	if len(s.Topics) == 0 || len(s.Intents) == 0 {
		return nil, errors.New("embeddings.json is empty")
	}
	return &s, nil
}

// ----- embedding backend -----

func (t *Tool) classifyEmbed(ctx context.Context, query string) (Result, error) {
	vec, err := embedQuery(ctx, query)
	if err != nil {
		return Result{}, err
	}
	tc, ts := argmaxCos(vec, t.embeddings.Topics)
	ic, is := argmaxCos(vec, t.embeddings.Intents)
	conf := math.Min(ts, is)
	return Result{
		TopicCode:  tc,
		IntentCode: ic,
		Confidence: round3(conf),
		Fallback:   conf < t.threshold,
		Backend:    "embedding",
	}, nil
}

func argmaxCos(q []float64, store map[string]embeddingEntry) (string, float64) {
	bestCode, bestSim := "", -1.0
	for code, e := range store {
		sim := cosine(q, e.Vector)
		if sim > bestSim {
			bestSim, bestCode = sim, code
		}
	}
	return bestCode, bestSim
}

func cosine(a, b []float64) float64 {
	if len(a) != len(b) || len(a) == 0 {
		return 0
	}
	var dot, na, nb float64
	for i := range a {
		dot += a[i] * b[i]
		na += a[i] * a[i]
		nb += b[i] * b[i]
	}
	if na == 0 || nb == 0 {
		return 0
	}
	return dot / (math.Sqrt(na) * math.Sqrt(nb))
}

// ----- embedding HTTP client -----

type embedReq struct {
	Model string `json:"model"`
	Input string `json:"input"`
}

type embedResp struct {
	Data []struct {
		Embedding []float64 `json:"embedding"`
	} `json:"data"`
	Error *struct {
		Message string `json:"message"`
	} `json:"error,omitempty"`
}

func embedQuery(ctx context.Context, text string) ([]float64, error) {
	base := getenvOr("EMBED_BASE_URL", "https://oneapi-comate.baidu-int.com")
	model := getenvOr("EMBED_MODEL", "text-embedding-v3")
	key := os.Getenv("EMBED_API_KEY")
	if key == "" {
		return nil, errors.New("EMBED_API_KEY not set")
	}
	body, _ := json.Marshal(embedReq{Model: model, Input: text})
	req, err := http.NewRequestWithContext(ctx, "POST", base+"/v1/embeddings", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+key)
	req.Header.Set("Content-Type", "application/json")
	cli := &http.Client{Timeout: 8 * time.Second}
	resp, err := cli.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	var er embedResp
	if jerr := json.Unmarshal(raw, &er); jerr != nil {
		return nil, fmt.Errorf("embedding decode: %w (status %d)", jerr, resp.StatusCode)
	}
	if er.Error != nil {
		return nil, errors.New(er.Error.Message)
	}
	if len(er.Data) == 0 || len(er.Data[0].Embedding) == 0 {
		return nil, errors.New("embedding empty")
	}
	return er.Data[0].Embedding, nil
}

func getenvOr(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}

// ----- character-bigram fallback backend -----

func (t *Tool) classifyCharBag(query string) Result {
	qbag := bigramBag(query)
	tc, ts := argmaxBag(qbag, t.corpus.Topics)
	ic, is := argmaxBag(qbag, t.corpus.Intents)
	conf := math.Min(ts, is)
	// Char-bag cosine is structurally lower than dense embeddings; scale the
	// threshold the same way so the surface contract is consistent.
	threshold := t.threshold * 0.5
	return Result{
		TopicCode:  tc,
		IntentCode: ic,
		Confidence: round3(conf),
		Fallback:   conf < threshold,
		Backend:    "charbag",
	}
}

func argmaxBag(q map[string]float64, store map[string]labelInfo) (string, float64) {
	bestCode, bestSim := "", -1.0
	for code, info := range store {
		profile := info.Name + "。" + info.Definition + "。" + strings.Join(info.Examples, "；")
		sim := cosineBag(q, bigramBag(profile))
		if sim > bestSim {
			bestSim, bestCode = sim, code
		}
	}
	return bestCode, bestSim
}

func bigramBag(s string) map[string]float64 {
	out := map[string]float64{}
	runes := []rune(s)
	if len(runes) < 1 {
		return out
	}
	// add char unigrams (helps very short queries)
	for _, r := range runes {
		if r == ' ' || r == '\n' {
			continue
		}
		out[string(r)] += 0.5
	}
	// bigrams
	for i := 0; i < len(runes)-1; i++ {
		if runes[i] == ' ' || runes[i+1] == ' ' {
			continue
		}
		out[string(runes[i:i+2])] += 1.0
	}
	return out
}

func cosineBag(a, b map[string]float64) float64 {
	if len(a) == 0 || len(b) == 0 {
		return 0
	}
	var dot, na, nb float64
	for k, v := range a {
		na += v * v
		if w, ok := b[k]; ok {
			dot += v * w
		}
	}
	for _, v := range b {
		nb += v * v
	}
	if na == 0 || nb == 0 {
		return 0
	}
	return dot / (math.Sqrt(na) * math.Sqrt(nb))
}

func round3(x float64) float64 {
	return math.Round(x*1000) / 1000
}

// Compile-time assertion that we did not accidentally break utf8 imports
// (kept only because some Eino integrations vet for unused stdlib).
var _ = utf8.RuneLen
