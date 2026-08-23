package audit

import (
	"encoding/json"
	"reflect"
	"sync"
	"testing"
	"time"

	"agentcanvas/internal/domain"
	"gorm.io/gorm/schema"
)

func TestNewLogEncodesDetailAndClient(t *testing.T) {
	log := NewLog(1, 2, "resource.update", "resource", "3", map[string]any{"ok": true}, "127.0.0.1", "test")
	if log.OwnerID != 1 || log.ActorID != 2 || string(log.DetailJSON) != `{"ok":true}` || log.IPAddress != "127.0.0.1" || log.UserAgent != "test" {
		t.Fatalf("NewLog() = %+v", log)
	}
	if got := NewLog(1, 1, "resource.read", "resource", "3", nil, "", "").DetailJSON; string(got) != "{}" {
		t.Fatalf("nil detail JSON = %q, want {}", got)
	}
}

func TestBaseModelsRemainFlatForJSONAndGORM(t *testing.T) {
	type fixture struct {
		domain.SoftDeleteModel
		Name string `json:"name" gorm:"column:name"`
	}
	now := time.Now().UTC()
	value := fixture{SoftDeleteModel: domain.SoftDeleteModel{
		BaseModel: domain.BaseModel{ID: 7, OwnerID: 9, CreatedAt: now, UpdatedAt: now},
	}}
	payload, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	var fields map[string]any
	if err := json.Unmarshal(payload, &fields); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"id", "owner_id", "created_at", "updated_at"} {
		if _, ok := fields[key]; !ok {
			t.Fatalf("embedded field %q was not flattened in JSON: %s", key, payload)
		}
	}
	parsed, err := schema.Parse(&fixture{}, &sync.Map{}, schema.NamingStrategy{})
	if err != nil {
		t.Fatal(err)
	}
	for _, column := range []string{"id", "owner_id", "created_at", "updated_at", "deleted_at", "name"} {
		if _, ok := parsed.FieldsByDBName[column]; !ok {
			t.Fatalf("embedded field %q was not flattened for GORM", column)
		}
	}
	if reflect.TypeOf(fixture{}).Field(0).Anonymous == false {
		t.Fatal("base model must be anonymously embedded")
	}
}
