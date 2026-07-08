package contextcompress

import (
	"strings"
	"testing"
)

func TestSelectScoresRepeatedMessagesWithLowerNovelty(t *testing.T) {
	items := []Item{
		{ID: 1, Content: "alpha beta gamma delta", Tokens: 5, Turn: 1},
		{ID: 2, Content: "alpha beta gamma delta", Tokens: 5, Turn: 2},
		{ID: 3, Content: "new incident timeline contains fresh deployment evidence", Tokens: 8, Turn: 3},
	}

	result := Select(items, Options{Budget: 20, MinReferenceLength: 3})
	if len(result.Scores) != len(items) {
		t.Fatalf("expected %d scores, got %d", len(items), len(result.Scores))
	}
	if result.Scores[1].Novelty >= result.Scores[0].Novelty {
		t.Fatalf("expected repeated message novelty to decrease, first=%f second=%f", result.Scores[0].Novelty, result.Scores[1].Novelty)
	}
	if result.Scores[2].Novelty <= result.Scores[1].Novelty {
		t.Fatalf("expected fresh message to outrank duplicate novelty, fresh=%f duplicate=%f", result.Scores[2].Novelty, result.Scores[1].Novelty)
	}
}

func TestSelectRespectsBudget(t *testing.T) {
	items := []Item{
		{ID: 1, Content: strings.Repeat("a unique block ", 8), Tokens: 10, Turn: 1},
		{ID: 2, Content: strings.Repeat("another unique block ", 8), Tokens: 10, Turn: 2},
		{ID: 3, Content: strings.Repeat("third unique block ", 8), Tokens: 10, Turn: 3},
	}

	result := Select(items, Options{Budget: 20})
	used := 0
	for _, item := range result.Selected {
		used += itemCost(item.Item)
	}
	if used > 20 {
		t.Fatalf("selected cost exceeded budget: %d", used)
	}
	if len(result.Selected) == 0 {
		t.Fatal("expected at least one selected item")
	}
}

func TestSelectKeepsDiverseFreshItems(t *testing.T) {
	items := []Item{
		{ID: 1, Content: "login failure timeout retry timeout retry timeout retry", Tokens: 6, Turn: 1},
		{ID: 2, Content: "login failure timeout retry timeout retry timeout retry", Tokens: 6, Turn: 2},
		{ID: 3, Content: "billing webhook signature verification regression", Tokens: 6, Turn: 3},
	}

	result := Select(items, Options{Budget: 12, DiversityLambda: 0.5, MinReferenceLength: 4})
	selected := map[int]bool{}
	for _, item := range result.Selected {
		selected[item.Item.ID] = true
	}
	if !selected[3] {
		t.Fatalf("expected diverse fresh item to be selected, got %+v", result.Selected)
	}
	if selected[1] && selected[2] {
		t.Fatalf("expected budgeted selector not to spend all budget on near duplicates, got %+v", result.Selected)
	}
}

func TestLongestCommonSubstringSimilarity(t *testing.T) {
	similar := longestCommonSubstringSimilarity("same chunk content with suffix", "prefix same chunk content")
	different := longestCommonSubstringSimilarity("alpha beta gamma", "network socket failure")
	if similar <= different {
		t.Fatalf("expected similar text score %f to exceed different text score %f", similar, different)
	}
	if similar <= 0 {
		t.Fatalf("expected positive similarity, got %f", similar)
	}
}

func TestNormalizeOptionsAllowsZeroToDisableScoringTerms(t *testing.T) {
	items := []Item{
		{ID: 1, Content: "old but important deployment failure root cause", Tokens: 6, Turn: 1},
		{ID: 2, Content: "new deployment failure root cause", Tokens: 5, Turn: 5},
	}
	scores := scoreItems(items, Options{Alpha: 0, DiversityLambda: 0})
	if scores[0].TimeDecay != 1 {
		t.Fatalf("expected Alpha=0 to disable time decay, got %f", scores[0].TimeDecay)
	}
	opts := normalizeOptions(Options{DiversityLambda: 0})
	if opts.DiversityLambda != 0 {
		t.Fatalf("expected DiversityLambda=0 to remain disabled, got %f", opts.DiversityLambda)
	}
}

func TestShortRepeatedMessagesLoseNovelty(t *testing.T) {
	items := []Item{
		{ID: 1, Content: "OK", Tokens: 1, Turn: 1},
		{ID: 2, Content: "OK", Tokens: 1, Turn: 2},
		{ID: 3, Content: "是的", Tokens: 2, Turn: 3},
		{ID: 4, Content: "是的", Tokens: 2, Turn: 4},
	}
	scores := scoreItems(items, Options{})
	if scores[1].Novelty >= scores[0].Novelty {
		t.Fatalf("expected repeated short English message novelty to drop: first=%f second=%f", scores[0].Novelty, scores[1].Novelty)
	}
	if scores[3].Novelty >= scores[2].Novelty {
		t.Fatalf("expected repeated short Chinese message novelty to drop: first=%f second=%f", scores[2].Novelty, scores[3].Novelty)
	}
}

func TestChineseSimilarityUsesCJKShingles(t *testing.T) {
	items := []Item{
		{ID: 1, Content: "今天天气很好，我们继续修复上下文压缩算法。"},
		{ID: 2, Content: "今天天气不错，我们继续修复上下文压缩器。"},
		{ID: 3, Content: "数据库连接失败，需要重新检查迁移脚本。"},
	}
	sim := buildSimilarityMatrix(items)
	if sim[0][1] <= sim[0][2] {
		t.Fatalf("expected related Chinese sentences to be more similar: related=%f unrelated=%f", sim[0][1], sim[0][2])
	}
}

func TestPinnedRepeatedInstructionIsKept(t *testing.T) {
	items := []Item{
		{ID: 1, Content: "请始终保持诚实", Tokens: 4, Turn: 1},
		{ID: 2, Content: "请始终保持诚实", Tokens: 4, Turn: 2, Pinned: true, Importance: 3},
		{ID: 3, Content: "普通闲聊内容", Tokens: 4, Turn: 3},
	}
	selection := Select(items, Options{Budget: 4})
	selected := map[int]bool{}
	for _, item := range selection.Selected {
		selected[item.Item.ID] = true
	}
	if !selected[2] {
		t.Fatalf("expected pinned repeated instruction to be kept, got %+v", selection.Selected)
	}
}

func TestCompressExtractsSummaryFragments(t *testing.T) {
	items := []Item{
		{ID: 1, Content: "用户要求压缩器不能依赖大模型。必须使用纯数学和算法。", Tokens: 24, Turn: 1, Importance: 2},
		{ID: 2, Content: "Alpha=0 必须真正关闭时间衰减，DiversityLambda=0 必须关闭多样性惩罚。", Tokens: 28, Turn: 2, Importance: 2},
		{ID: 3, Content: "OK", Tokens: 1, Turn: 3},
	}
	compression := Compress(items, Options{Budget: 1, SummaryBudget: 40, DiversityLambda: 0.35})
	if compression.Summary == "" {
		t.Fatal("expected non-empty extractive summary")
	}
	if !strings.Contains(compression.Summary, "Alpha=0") && !strings.Contains(compression.Summary, "纯数学") {
		t.Fatalf("expected summary to retain key constraints, got %q", compression.Summary)
	}
}
