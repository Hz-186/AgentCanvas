package chat_usecase

import "fmt"

const emptyContextText = "（没有检索到可用的知识库上下文）"

type PromptBuilder struct{}

func NewPromptBuilder() *PromptBuilder {
	return &PromptBuilder{}
}

func (b *PromptBuilder) Build(question, contextText string) string {
	if contextText == "" {
		contextText = emptyContextText
	}
	return fmt.Sprintf(`你是一个严谨的知识库问答助手。
请基于给定的知识库上下文回答用户问题。
如果上下文中没有答案，请明确说“不知道”，不要编造。
回答时尽量给出引用依据。

【知识库上下文】
%s

【用户问题】
%s`, contextText, question)
}
