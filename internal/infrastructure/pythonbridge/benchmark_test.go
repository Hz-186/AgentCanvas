package pythonbridge

import (
	"context"
	"crypto/sha256"
	_ "embed"
	"encoding/json"
	"fmt"
	"os"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"

	"agentcanvas/internal/domain/retrieval"
	"agentcanvas/internal/infrastructure/chunker"
	"agentcanvas/internal/infrastructure/parser"
	esretrieval "agentcanvas/internal/infrastructure/retrieval/elasticsearch"
	"agentcanvas/internal/pkg/config"
	esclient "github.com/elastic/go-elasticsearch/v8"
)

// benchmarkFixtures is deliberately checked in so Go and Python comparisons
// keep using the same documents as the bridge evolves.
//
//go:embed testdata/fixtures.json
var benchmarkFixtures []byte

type benchmarkFixture struct {
	Name             string           `json:"name"`
	Method           string           `json:"method"`
	ChunkSize        int              `json:"chunk_size"`
	Overlap          int              `json:"overlap"`
	Text             string           `json:"text"`
	RepeatText       string           `json:"repeat_text"`
	RepeatCount      int              `json:"repeat_count"`
	Blocks           []benchmarkBlock `json:"blocks"`
	Queries          []benchmarkQuery `json:"queries"`
	RequiredMetadata []string         `json:"required_metadata"`
}

type benchmarkQuery struct {
	Term     string   `json:"term"`
	Expected []string `json:"expected"`
}

type benchmarkBlock struct {
	ID       string         `json:"id"`
	Type     string         `json:"type"`
	Text     string         `json:"text"`
	PageNo   *int           `json:"page_no"`
	Metadata map[string]any `json:"metadata"`
}

func TestLivePythonBridgeBenchmark(t *testing.T) {
	target := os.Getenv("AGENTCANVAS_PYTHON_BRIDGE_TEST_TARGET")
	if target == "" {
		t.Skip("AGENTCANVAS_PYTHON_BRIDGE_TEST_TARGET is not set")
	}
	var fixtures []benchmarkFixture
	if err := json.Unmarshal(benchmarkFixtures, &fixtures); err != nil {
		t.Fatal(err)
	}
	bridge, err := NewClient(Config{
		Enabled: true, Target: target, AuthToken: os.Getenv("AGENTCANVAS_PYTHON_BRIDGE_TOKEN"),
		ConnectTimeout: 3 * time.Second, RequestTimeout: 3 * time.Second,
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = bridge.Close() })
	native := chunker.NewDefaultRegistry()
	esStore, esClient, esIndex := newBenchmarkRetrievalStore(t)
	if esStore == nil {
		t.Log("AGENTCANVAS_ELASTICSEARCH_URL is not set; using deterministic retrieval proxy metrics")
	}
	if esClient != nil {
		t.Cleanup(func() {
			response, deleteErr := esClient.Indices.Delete([]string{esIndex})
			if deleteErr == nil {
				response.Body.Close()
			}
		})
	}
	for fixtureIndex, fixture := range fixtures {
		fixture := fixture
		t.Run(fixture.Name, func(t *testing.T) {
			doc := fixture.document()
			policy := chunker.Policy{ChunkSize: fixture.ChunkSize, Overlap: fixture.Overlap}
			goChunks, err := native.Chunk(context.Background(), fixture.Method, doc, policy)
			if err != nil {
				t.Fatal(err)
			}
			pythonChunks, err := bridge.ChunkDocument(context.Background(), "python:"+fixture.Method, doc, policy)
			if err != nil {
				t.Fatal(err)
			}
			if len(goChunks) != len(pythonChunks) {
				t.Fatalf("chunk count differs: go=%d python=%d", len(goChunks), len(pythonChunks))
			}
			if ratio := boundaryMatchRatio(goChunks, pythonChunks); len(goChunks) > 0 && ratio < 0.5 {
				t.Fatalf("boundary match ratio = %.2f, want at least 0.50", ratio)
			}
			for _, key := range fixture.RequiredMetadata {
				found := false
				for _, item := range pythonChunks {
					if _, ok := item.Metadata[key]; ok {
						found = true
						break
					}
				}
				if !found {
					t.Errorf("Python chunks did not retain metadata key %q", key)
				}
			}
			recall, precision := retrievalMetrics(pythonChunks, fixture.Queries, 3)
			backend := "proxy"
			if esStore != nil {
				backend = "elasticsearch"
				kbID := int64(1000 + fixtureIndex)
				if err := indexBenchmarkChunks(context.Background(), esStore, pythonChunks, kbID); err != nil {
					t.Fatal(err)
				}
				recall, precision, err = elasticsearchMetrics(context.Background(), esStore, pythonChunks, fixture.Queries, kbID)
				if err != nil {
					t.Fatal(err)
				}
			}
			t.Logf("retrieval backend=%s recall@3=%.2f precision@3=%.2f", backend, recall, precision)
			if len(fixture.Queries) > 0 && (recall < 0.5 || precision < 0.5) {
				t.Fatalf("retrieval proxy metrics too low: recall=%.2f precision=%.2f", recall, precision)
			}
			measureBridge(t, bridge, fixture, doc, policy)
		})
	}
}

func newBenchmarkRetrievalStore(t *testing.T) (*esretrieval.Store, *esclient.Client, string) {
	endpoint := strings.TrimSpace(os.Getenv("AGENTCANVAS_ELASTICSEARCH_URL"))
	if endpoint == "" {
		return nil, nil, ""
	}
	index := "agentcanvas_python_bridge_benchmark_" + strconv.FormatInt(time.Now().UnixNano(), 10)
	client, err := esclient.NewClient(esclient.Config{Addresses: []string{endpoint}})
	if err != nil {
		t.Fatalf("create Elasticsearch benchmark client: %v", err)
	}
	store := esretrieval.NewStore(client, config.ElasticsearchConfig{ChunkIndex: index})
	if err := store.EnsureIndex(context.Background()); err != nil {
		t.Fatalf("ensure Elasticsearch benchmark index: %v", err)
	}
	return store, client, index
}

func indexBenchmarkChunks(ctx context.Context, store *esretrieval.Store, chunks []chunker.Chunk, kbID int64) error {
	documents := make([]retrieval.ChunkIndexDocument, 0, len(chunks))
	for index, chunk := range chunks {
		hash := sha256.Sum256([]byte(chunk.Content))
		now := time.Now().UTC()
		documents = append(documents, retrieval.ChunkIndexDocument{
			OwnerID: 1, KBID: kbID, DocumentID: kbID, ChunkID: kbID*1000 + int64(index+1), ChunkIndex: index,
			DocumentName: "python-bridge-benchmark", FileType: "md", Content: chunk.Content,
			ContentHash: fmt.Sprintf("%x", hash[:]), Enabled: true, PageNo: chunk.PageNo, TokenCount: chunk.TokenCount,
			Metadata: chunk.Metadata, CreatedAt: now, UpdatedAt: now,
		})
	}
	if len(documents) == 0 {
		return nil
	}
	return store.IndexChunks(ctx, documents)
}

func elasticsearchMetrics(ctx context.Context, store *esretrieval.Store, chunks []chunker.Chunk, queries []benchmarkQuery, kbID int64) (float64, float64, error) {
	if len(queries) == 0 {
		return 1, 1, nil
	}
	var recall, precision float64
	measured := 0
	for _, query := range queries {
		response, err := store.Search(ctx, retrieval.RetrievalRequest{OwnerID: 1, KBIDs: []int64{kbID}, Query: query.Term, TopK: 3, Mode: retrieval.ModeKeyword})
		if err != nil {
			return 0, 0, err
		}
		relevant := 0
		for _, chunk := range chunks {
			if containsExpected(chunk.Content, query.Expected) {
				relevant++
			}
		}
		if relevant == 0 {
			continue
		}
		hits := 0
		for _, result := range response.Results {
			if containsExpected(result.Content, query.Expected) {
				hits++
			}
		}
		recall += float64(hits) / float64(relevant)
		if len(response.Results) > 0 {
			precision += float64(hits) / float64(len(response.Results))
		}
		measured++
	}
	if measured == 0 {
		return 1, 1, nil
	}
	return recall / float64(measured), precision / float64(measured), nil
}

func containsExpected(content string, expected []string) bool {
	content = strings.ToLower(content)
	for _, item := range expected {
		if strings.Contains(content, strings.ToLower(item)) {
			return true
		}
	}
	return false
}

func (f benchmarkFixture) document() parser.ParsedDocument {
	text := f.Text
	if f.RepeatCount > 0 {
		text += strings.Repeat(f.RepeatText, f.RepeatCount)
	}
	blocks := make([]parser.DocumentBlock, 0, len(f.Blocks))
	for _, block := range f.Blocks {
		blocks = append(blocks, parser.DocumentBlock{ID: block.ID, Type: block.Type, Text: block.Text, PageNo: block.PageNo, Metadata: block.Metadata})
	}
	return parser.ParsedDocument{Text: text, FileType: "md", Blocks: blocks}
}

func measureBridge(t *testing.T, bridge *Client, fixture benchmarkFixture, doc parser.ParsedDocument, policy chunker.Policy) {
	const samples = 15
	durations := make([]time.Duration, 0, samples)
	var before, after runtime.MemStats
	runtime.ReadMemStats(&before)
	for index := 0; index < samples; index++ {
		started := time.Now()
		if _, err := bridge.ChunkDocument(context.Background(), "python:"+fixture.Method, doc, policy); err != nil {
			t.Fatal(err)
		}
		durations = append(durations, time.Since(started))
	}
	runtime.ReadMemStats(&after)
	sort.Slice(durations, func(i, j int) bool { return durations[i] < durations[j] })
	t.Logf("python bridge benchmark p50=%s p95=%s client_alloc_bytes=%d", durations[len(durations)/2], durations[int(float64(len(durations)-1)*0.95)], after.TotalAlloc-before.TotalAlloc)
}

func boundaryMatchRatio(goChunks, pythonChunks []chunker.Chunk) float64 {
	if len(goChunks) == 0 && len(pythonChunks) == 0 {
		return 1
	}
	denominator := len(goChunks)
	if len(pythonChunks) > denominator {
		denominator = len(pythonChunks)
	}
	matched := 0
	for index := 0; index < len(goChunks) && index < len(pythonChunks); index++ {
		if goChunks[index].Content == pythonChunks[index].Content && goChunks[index].SectionTitle == pythonChunks[index].SectionTitle {
			matched++
		}
	}
	return float64(matched) / float64(denominator)
}

func retrievalMetrics(chunks []chunker.Chunk, queries []benchmarkQuery, topK int) (float64, float64) {
	if len(queries) == 0 {
		return 1, 1
	}
	var recall, precision float64
	measured := 0
	for _, query := range queries {
		term := strings.ToLower(strings.TrimSpace(query.Term))
		if term == "" {
			continue
		}
		relevant := make(map[int]struct{})
		for index, item := range chunks {
			content := strings.ToLower(item.Content)
			for _, expected := range query.Expected {
				if strings.Contains(content, strings.ToLower(expected)) {
					relevant[index] = struct{}{}
				}
			}
		}
		if len(relevant) == 0 {
			continue
		}
		indices := make([]int, 0, len(chunks))
		for index, item := range chunks {
			if score := strings.Count(strings.ToLower(item.Content), term); score > 0 {
				indices = append(indices, index)
			}
		}
		sort.SliceStable(indices, func(i, j int) bool {
			left := strings.Count(strings.ToLower(chunks[indices[i]].Content), term)
			right := strings.Count(strings.ToLower(chunks[indices[j]].Content), term)
			return left > right
		})
		if len(indices) > topK {
			indices = indices[:topK]
		}
		hits := 0
		for _, index := range indices {
			if _, ok := relevant[index]; ok {
				hits++
			}
		}
		recall += float64(hits) / float64(len(relevant))
		if len(indices) > 0 {
			precision += float64(hits) / float64(len(indices))
		}
		measured++
	}
	if measured == 0 {
		return 1, 1
	}
	return recall / float64(measured), precision / float64(measured)
}
