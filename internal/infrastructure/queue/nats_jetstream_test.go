package queue

import (
	"context"
	"encoding/json"
	"testing"
	"time"
)

func TestNATSJetStreamQueuePublishesClaimsAndAcks(t *testing.T) {
	client := &fakeNATSJetStream{}
	q := NewNATSJetStreamQueue(client, "jobs", "jobs.ingestion", "workers", "workers", time.Minute)

	job := Job{ID: "job-1", Type: "ingestion", Payload: map[string]any{"document_id": float64(20)}}
	if err := q.Publish(context.Background(), job); err != nil {
		t.Fatalf("Publish() error = %v", err)
	}
	if client.publishedSubject != "jobs.ingestion" || client.publishedMsgID != "job-1" {
		t.Fatalf("publish subject/msgID = %s/%s", client.publishedSubject, client.publishedMsgID)
	}
	var envelope Job
	if err := json.Unmarshal(client.publishedData, &envelope); err != nil || envelope.SchemaVersion != JobSchemaVersion {
		t.Fatalf("published envelope = %+v, error=%v", envelope, err)
	}
	client.messages = []NATSMessage{&fakeNATSMessage{data: client.publishedData}}

	claimed, err := q.Claim(context.Background(), ClaimOptions{WorkerID: "worker-1", Limit: 1})
	if err != nil {
		t.Fatalf("Claim() error = %v", err)
	}
	if len(claimed) != 1 || claimed[0].ID != "job-1" || claimed[0].AttemptCount != 1 {
		t.Fatalf("claimed = %+v", claimed)
	}
	if err := q.Ack(context.Background(), "job-1"); err != nil {
		t.Fatalf("Ack() error = %v", err)
	}
	msg := client.messages[0].(*fakeNATSMessage)
	if !msg.acked {
		t.Fatal("message was not acked")
	}
}

func TestNATSJetStreamQueueNacksFutureJobWithDelay(t *testing.T) {
	future := time.Now().Add(time.Hour)
	data, _ := json.Marshal(Job{ID: "future", Type: "ingestion", AvailableAt: future})
	msg := &fakeNATSMessage{data: data}
	client := &fakeNATSJetStream{messages: []NATSMessage{msg}}
	q := NewNATSJetStreamQueue(client, "jobs", "jobs.ingestion", "workers", "workers", time.Minute)

	claimed, err := q.Claim(context.Background(), ClaimOptions{Limit: 1, Now: time.Now()})
	if err != nil {
		t.Fatalf("Claim() error = %v", err)
	}
	if len(claimed) != 0 {
		t.Fatalf("future job should not be claimed: %+v", claimed)
	}
	if msg.delay <= 0 {
		t.Fatalf("expected delayed nack, got %v", msg.delay)
	}
}

func TestNATSJetStreamQueueNacksMalformedPayload(t *testing.T) {
	msg := &fakeNATSMessage{data: []byte("{")}
	client := &fakeNATSJetStream{messages: []NATSMessage{msg}}
	q := NewNATSJetStreamQueue(client, "jobs", "jobs.ingestion", "workers", "workers", time.Minute)

	_, err := q.Claim(context.Background(), ClaimOptions{Limit: 1})
	if err == nil {
		t.Fatal("Claim() error = nil, want malformed payload error")
	}
	if !msg.nacked {
		t.Fatal("malformed message was not nacked")
	}
}

func TestNATSJetStreamQueueRejectsUnsupportedEnvelopeVersion(t *testing.T) {
	data, _ := json.Marshal(Job{SchemaVersion: JobSchemaVersion + 1, ID: "future", Type: "ingestion"})
	msg := &fakeNATSMessage{data: data}
	client := &fakeNATSJetStream{messages: []NATSMessage{msg}}
	q := NewNATSJetStreamQueue(client, "jobs", "jobs.ingestion", "workers", "workers", time.Minute)

	if _, err := q.Claim(context.Background(), ClaimOptions{Limit: 1}); err == nil || !msg.nacked {
		t.Fatalf("unknown envelope version error=%v nacked=%v", err, msg.nacked)
	}
}

func TestNATSJetStreamQueueNackUsesDelay(t *testing.T) {
	data, _ := json.Marshal(Job{ID: "retry", Type: "ingestion"})
	msg := &fakeNATSMessage{data: data}
	client := &fakeNATSJetStream{messages: []NATSMessage{msg}}
	q := NewNATSJetStreamQueue(client, "jobs", "jobs.ingestion", "workers", "workers", time.Minute)

	claimed, err := q.Claim(context.Background(), ClaimOptions{Limit: 1})
	if err != nil || len(claimed) != 1 {
		t.Fatalf("Claim() = %+v, %v", claimed, err)
	}
	if err := q.Nack(context.Background(), "retry", time.Now().Add(time.Minute)); err != nil {
		t.Fatalf("Nack() error = %v", err)
	}
	if msg.delay <= 0 {
		t.Fatalf("expected delayed nack, got %v", msg.delay)
	}
}

func TestNATSJetStreamQueueHonorsJobMaxAttempts(t *testing.T) {
	data, _ := json.Marshal(Job{ID: "retry", Type: "ingestion", AttemptCount: 1, MaxAttempts: 2})
	msg := &fakeNATSMessage{data: data}
	client := &fakeNATSJetStream{messages: []NATSMessage{msg}}
	q := NewNATSJetStreamQueue(client, "jobs", "jobs.ingestion", "workers", "workers", time.Minute)

	claimed, err := q.Claim(context.Background(), ClaimOptions{Limit: 1})
	if err != nil || len(claimed) != 1 || claimed[0].AttemptCount != 2 {
		t.Fatalf("Claim() = %+v, %v", claimed, err)
	}
	if err := q.Nack(context.Background(), "retry", time.Now()); err != nil {
		t.Fatalf("Nack() error = %v", err)
	}
	if !msg.acked || msg.nacked || msg.delay != 0 {
		t.Fatalf("exhausted job delivery state = %+v", msg)
	}
}

type fakeNATSJetStream struct {
	streamEnsured    string
	subjectEnsured   string
	consumerEnsured  string
	publishedSubject string
	publishedData    []byte
	publishedMsgID   string
	messages         []NATSMessage
}

func (f *fakeNATSJetStream) EnsureStream(_ context.Context, stream, subject string) error {
	f.streamEnsured = stream
	f.subjectEnsured = subject
	return nil
}

func (f *fakeNATSJetStream) EnsureConsumer(_ context.Context, _ string, durable string, _ time.Duration) error {
	f.consumerEnsured = durable
	return nil
}

func (f *fakeNATSJetStream) Publish(_ context.Context, subject string, data []byte, msgID string) (string, error) {
	f.publishedSubject = subject
	f.publishedData = data
	f.publishedMsgID = msgID
	return "1", nil
}

func (f *fakeNATSJetStream) Fetch(context.Context, string, string, int, time.Duration) ([]NATSMessage, error) {
	return f.messages, nil
}

func (f *fakeNATSJetStream) Close() error { return nil }

type fakeNATSMessage struct {
	data   []byte
	acked  bool
	nacked bool
	delay  time.Duration
}

func (m *fakeNATSMessage) Data() []byte         { return m.data }
func (m *fakeNATSMessage) DeliveryAttempt() int { return 0 }
func (m *fakeNATSMessage) Ack() error {
	m.acked = true
	return nil
}
func (m *fakeNATSMessage) Nak() error {
	m.nacked = true
	return nil
}
func (m *fakeNATSMessage) NakWithDelay(delay time.Duration) error {
	m.delay = delay
	return nil
}
