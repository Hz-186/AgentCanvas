package llm

import "testing"

func TestProposedPlanStreamParserSupportsChunkedExclusiveLines(t *testing.T) {
	parser := &ProposedPlanStreamParser{}
	parser.Push("answer\n<proposed_")
	parser.Push("plan>\nstep one\n</proposed_plan>")
	parser.Finish()
	parserVisible, parserPlan := parser.VisiblePlan()
	if parserVisible != "answer\n" || parserPlan != "step one\n" {
		t.Fatalf("stream parser state = visible %q plan %q", parserVisible, parserPlan)
	}
	visible, plan := NormalizeProposedPlan("answer\n<proposed_plan>\nstep one\n</proposed_plan>\n")
	if visible != "answer\n" || plan != "step one\n" {
		t.Fatalf("normalized proposed plan = visible %q plan %q", visible, plan)
	}
	visible, plan = NormalizeProposedPlan("<proposed_plan>\na\n</proposed_plan>\n<proposed_plan>\nb\n</proposed_plan>")
	if visible != "" || plan != "b\n" {
		t.Fatalf("last proposed plan should win: visible %q plan %q", visible, plan)
	}
}

func TestProposedPlanParserKeepsInlineTagsVisible(t *testing.T) {
	visible, plan := NormalizeProposedPlan("inline <proposed_plan> tag")
	if visible != "inline <proposed_plan> tag" || plan != "" {
		t.Fatalf("inline tags must remain visible: visible %q plan %q", visible, plan)
	}
}
