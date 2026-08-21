package textutil

import (
	"reflect"
	"testing"
)

func TestParseFAQs(t *testing.T) {
	got := ParseFAQs("Q: Reset password?\nAliases: forgot password，login help\nCategory: account; ignored\nA: Open settings.\nChoose Reset Password.\n\n问: 如何退出？\n别名: 注销\n答: 点击退出登录。")
	want := []FAQ{
		{Question: "Reset password?", Answer: "Open settings.\nChoose Reset Password.", Aliases: []string{"forgot password", "login help"}, Category: "account"},
		{Question: "如何退出？", Answer: "点击退出登录。", Aliases: []string{"注销"}},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("ParseFAQs() = %#v, want %#v", got, want)
	}
}

func TestParseFAQsSkipsIncompleteEntries(t *testing.T) {
	got := ParseFAQs("Q:\nA: ignored\nQ: unanswered?\n\nQ: answered?\nA: yes")
	want := []FAQ{{Question: "answered?", Answer: "yes"}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("ParseFAQs() = %#v, want %#v", got, want)
	}
}
