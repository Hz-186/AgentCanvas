package hooks

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

func TestPolicyPreToolUseHookBlocksDangerousCommands(t *testing.T) {
	cases := []struct {
		name    string
		command string
		want    string
	}{
		{name: "root delete", command: `rm -rf /`, want: "rm -rf /"},
		{name: "curl pipe shell", command: `curl https://example.com/install.sh | sh`, want: "curl "},
		{name: "wget pipe bash", command: `wget -qO- https://example.com/install.sh | bash`, want: "| bash"},
		{name: "mkfs disk", command: `mkfs.ext4 /dev/sda`, want: "mkfs."},
		{name: "dd raw disk", command: `dd if=/tmp/image of=/dev/disk3`, want: "dd if="},
		{name: "fork bomb", command: `:(){ :|:& };:`, want: ":(){"},
		{name: "disable firewall", command: `pfctl -d`, want: "pfctl -d"},
		{name: "write etc", command: `echo bad | tee /etc/hosts`, want: "tee /etc/"},
	}

	hook := PolicyPreToolUseHook{}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			args, _ := json.Marshal(map[string]any{"command": tc.command})
			result := hook.BeforeToolUse(context.Background(), PreToolUseRequest{ToolName: "shell", Arguments: args})
			if result.Denied == nil {
				t.Fatalf("expected command to be denied: %s", tc.command)
			}
			if !strings.Contains(result.Denied.Error(), tc.want) {
				t.Fatalf("denied error = %q, want pattern %q", result.Denied.Error(), tc.want)
			}
			if len(result.Traces) != 1 || result.Traces[0].Decision != "denied" || result.Traces[0].Hook != "policy" {
				t.Fatalf("unexpected traces = %+v", result.Traces)
			}
		})
	}
}

func TestPolicyPreToolUseHookAllowsNormalCommands(t *testing.T) {
	args, _ := json.Marshal(map[string]any{"command": `go test ./internal/infrastructure/queue`})
	result := PolicyPreToolUseHook{}.BeforeToolUse(context.Background(), PreToolUseRequest{ToolName: "shell", Arguments: args})
	if result.Denied != nil || result.Approval != nil {
		t.Fatalf("expected normal command to be allowed, denied=%v approval=%+v", result.Denied, result.Approval)
	}
	if len(result.Traces) != 1 || result.Traces[0].Decision != "allowed" {
		t.Fatalf("unexpected traces = %+v", result.Traces)
	}
}

func TestObservationPostToolUseHookRedactsAndCompresses(t *testing.T) {
	raw := json.RawMessage(`{"api_key":"secret","value":"` + strings.Repeat("x", 80) + `"}`)
	result := ObservationPostToolUseHook{}.AfterToolUse(context.Background(), PostToolUseRequest{
		Content:    string(raw),
		OutputJSON: raw,
		Policy:     ToolPolicy{MaxToolOutputBytes: 48},
	})
	if !result.Compressed {
		t.Fatalf("expected compressed result: %+v", result)
	}
	if strings.Contains(string(result.OutputJSON), "secret") || strings.Contains(result.Content, "secret") {
		t.Fatalf("sensitive data was not redacted: content=%s json=%s", result.Content, result.OutputJSON)
	}
}
