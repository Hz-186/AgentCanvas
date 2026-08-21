package audit

import "testing"

func TestNewLogEncodesDetailAndClient(t *testing.T) {
	log := NewLog(1, 2, "resource.update", "resource", "3", map[string]any{"ok": true}, "127.0.0.1", "test")
	if log.OwnerID != 1 || log.ActorID != 2 || log.DetailJSON != `{"ok":true}` || log.IPAddress != "127.0.0.1" || log.UserAgent != "test" {
		t.Fatalf("NewLog() = %+v", log)
	}
	if got := NewLog(1, 1, "resource.read", "resource", "3", nil, "", "").DetailJSON; got != "{}" {
		t.Fatalf("nil detail JSON = %q, want {}", got)
	}
}
