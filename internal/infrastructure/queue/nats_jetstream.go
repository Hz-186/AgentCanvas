package queue

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"agentcanvas/internal/pkg/config"

	"github.com/nats-io/nats.go"
)

type NATSMessage interface {
	Data() []byte
	DeliveryAttempt() int
	Ack() error
	Nak() error
	NakWithDelay(time.Duration) error
}

type NATSJetStream interface {
	EnsureStream(ctx context.Context, stream, subject string) error
	EnsureConsumer(ctx context.Context, stream, durable string, ackWait time.Duration) error
	Publish(ctx context.Context, subject string, data []byte, msgID string) (string, error)
	Fetch(ctx context.Context, stream, durable string, limit int, maxWait time.Duration) ([]NATSMessage, error)
	Close() error
}

type natsConsumerDeliveryConfigurer interface {
	EnsureConsumerWithMax(ctx context.Context, stream, durable string, ackWait time.Duration, maxDeliver int) error
}

type natsInflight struct {
	message     NATSMessage
	attempts    int
	maxAttempts int
}

type NATSJetStreamQueue struct {
	Client      NATSJetStream
	Stream      string
	Subject     string
	Consumer    string
	Durable     string
	AckWait     time.Duration
	MaxWait     time.Duration
	MaxAttempts int

	mu       sync.Mutex
	inflight map[string]natsInflight
}

func NewNATSJetStreamQueue(client NATSJetStream, stream, subject, consumer, durable string, ackWait time.Duration) *NATSJetStreamQueue {
	if stream == "" {
		stream = "AGENTCANVAS_INGESTION"
	}
	if subject == "" {
		subject = "agentcanvas.ingestion"
	}
	if consumer == "" {
		consumer = "agentcanvas-workers"
	}
	if durable == "" {
		durable = consumer
	}
	if ackWait <= 0 {
		ackWait = time.Minute
	}
	return &NATSJetStreamQueue{Client: client, Stream: stream, Subject: subject, Consumer: consumer, Durable: durable, AckWait: ackWait, MaxWait: time.Second, MaxAttempts: 5, inflight: map[string]natsInflight{}}
}

func NewNATSJetStreamQueueFromConfig(cfg config.NATSConfig) (*NATSJetStreamQueue, error) {
	client, err := NewNATSJetStreamClient(cfg.URL)
	if err != nil {
		return nil, err
	}
	return NewNATSJetStreamQueue(client, cfg.Stream, cfg.Subject, cfg.Consumer, cfg.Durable, time.Duration(cfg.AckWaitSeconds)*time.Second), nil
}

func (q *NATSJetStreamQueue) Publish(ctx context.Context, job Job) error {
	if err := q.ensure(ctx); err != nil {
		return err
	}
	if job.Type == "" {
		return fmt.Errorf("job type is required")
	}
	if job.SchemaVersion == 0 {
		job.SchemaVersion = JobSchemaVersion
	}
	if job.SchemaVersion != JobSchemaVersion {
		return fmt.Errorf("unsupported job schema version %d", job.SchemaVersion)
	}
	data, err := json.Marshal(job)
	if err != nil {
		return err
	}
	msgID := strings.TrimSpace(job.ID)
	if msgID == "" {
		msgID = fmt.Sprintf("%s:%d", job.Type, time.Now().UnixNano())
	}
	_, err = q.Client.Publish(ctx, q.Subject, data, msgID)
	return err
}

func (q *NATSJetStreamQueue) Claim(ctx context.Context, opts ClaimOptions) ([]Job, error) {
	if err := q.ensure(ctx); err != nil {
		return nil, err
	}
	limit := opts.Limit
	if limit <= 0 {
		limit = 1
	}
	messages, err := q.Client.Fetch(ctx, q.Stream, q.Durable, limit, q.MaxWait)
	if err != nil {
		return nil, err
	}
	now := opts.Now
	if now.IsZero() {
		now = time.Now()
	}
	jobs := make([]Job, 0, len(messages))
	for _, message := range messages {
		var job Job
		if err := json.Unmarshal(message.Data(), &job); err != nil {
			_ = message.Nak()
			return nil, err
		}
		if job.SchemaVersion == 0 {
			job.SchemaVersion = JobSchemaVersion
		}
		if job.SchemaVersion != JobSchemaVersion {
			_ = message.Nak()
			return nil, fmt.Errorf("unsupported job schema version %d", job.SchemaVersion)
		}
		if job.ID == "" {
			job.ID = fmt.Sprintf("nats:%p", message)
		}
		if !job.AvailableAt.IsZero() && job.AvailableAt.After(now) {
			_ = message.NakWithDelay(time.Until(job.AvailableAt))
			continue
		}
		if deliveryAttempt := message.DeliveryAttempt(); deliveryAttempt > 0 {
			job.Attempts = deliveryAttempt
		} else {
			job.Attempts++
		}
		if job.MaxAttempts == 0 {
			job.MaxAttempts = q.MaxAttempts
		}
		q.mu.Lock()
		q.inflight[job.ID] = natsInflight{message: message, attempts: job.Attempts, maxAttempts: job.MaxAttempts}
		q.mu.Unlock()
		jobs = append(jobs, job)
	}
	return jobs, nil
}

func (q *NATSJetStreamQueue) Ack(ctx context.Context, jobID string) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	default:
	}
	delivery, err := q.popInflight(jobID)
	if err != nil {
		return err
	}
	return delivery.message.Ack()
}

func (q *NATSJetStreamQueue) Nack(ctx context.Context, jobID string, retryAt time.Time) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	default:
	}
	delivery, err := q.popInflight(jobID)
	if err != nil {
		return err
	}
	if delivery.maxAttempts > 0 && delivery.attempts >= delivery.maxAttempts {
		return delivery.message.Ack()
	}
	if retryAt.After(time.Now()) {
		return delivery.message.NakWithDelay(time.Until(retryAt))
	}
	return delivery.message.Nak()
}

func (q *NATSJetStreamQueue) Close() error {
	if q == nil || q.Client == nil {
		return nil
	}
	return q.Client.Close()
}

func (q *NATSJetStreamQueue) ensure(ctx context.Context) error {
	if q == nil || q.Client == nil {
		return fmt.Errorf("nats jetstream queue is not configured")
	}
	if err := q.Client.EnsureStream(ctx, q.Stream, q.Subject); err != nil {
		return err
	}
	if configured, ok := q.Client.(natsConsumerDeliveryConfigurer); ok {
		return configured.EnsureConsumerWithMax(ctx, q.Stream, q.Durable, q.AckWait, q.MaxAttempts)
	}
	return q.Client.EnsureConsumer(ctx, q.Stream, q.Durable, q.AckWait)
}

func (q *NATSJetStreamQueue) popInflight(jobID string) (natsInflight, error) {
	q.mu.Lock()
	defer q.mu.Unlock()
	delivery, ok := q.inflight[jobID]
	if !ok {
		return natsInflight{}, fmt.Errorf("claimed nats job %s not found", jobID)
	}
	delete(q.inflight, jobID)
	return delivery, nil
}

type natsJetStreamClient struct {
	conn *nats.Conn
	js   nats.JetStreamContext

	mu   sync.Mutex
	subs map[string]*nats.Subscription
}

func NewNATSJetStreamClient(url string) (NATSJetStream, error) {
	url = strings.TrimSpace(url)
	if url == "" {
		url = nats.DefaultURL
	}
	conn, err := nats.Connect(url)
	if err != nil {
		return nil, err
	}
	js, err := conn.JetStream()
	if err != nil {
		conn.Close()
		return nil, err
	}
	return &natsJetStreamClient{conn: conn, js: js, subs: map[string]*nats.Subscription{}}, nil
}

func (c *natsJetStreamClient) EnsureStream(_ context.Context, stream, subject string) error {
	if _, err := c.js.StreamInfo(stream); err == nil {
		return nil
	}
	_, err := c.js.AddStream(&nats.StreamConfig{Name: stream, Subjects: []string{subject}, Storage: nats.FileStorage, Retention: nats.WorkQueuePolicy})
	return err
}

func (c *natsJetStreamClient) EnsureConsumer(_ context.Context, stream, durable string, ackWait time.Duration) error {
	return c.EnsureConsumerWithMax(context.Background(), stream, durable, ackWait, 5)
}

func (c *natsJetStreamClient) EnsureConsumerWithMax(_ context.Context, stream, durable string, ackWait time.Duration, maxDeliver int) error {
	if _, err := c.js.ConsumerInfo(stream, durable); err == nil {
		return nil
	}
	_, err := c.js.AddConsumer(stream, &nats.ConsumerConfig{Durable: durable, AckPolicy: nats.AckExplicitPolicy, AckWait: ackWait, MaxDeliver: maxDeliver})
	return err
}

func (c *natsJetStreamClient) Publish(ctx context.Context, subject string, data []byte, msgID string) (string, error) {
	ack, err := c.js.Publish(subject, data, nats.Context(ctx), nats.MsgId(msgID))
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("%d", ack.Sequence), nil
}

func (c *natsJetStreamClient) Fetch(ctx context.Context, stream, durable string, limit int, maxWait time.Duration) ([]NATSMessage, error) {
	sub, err := c.pullSubscription(stream, durable)
	if err != nil {
		return nil, err
	}
	fetchCtx, cancel := context.WithTimeout(ctx, maxWait)
	defer cancel()
	messages, err := sub.Fetch(limit, nats.Context(fetchCtx))
	if err != nil {
		if errors.Is(err, nats.ErrTimeout) || errors.Is(err, context.DeadlineExceeded) {
			return nil, nil
		}
		return nil, err
	}
	out := make([]NATSMessage, 0, len(messages))
	for _, message := range messages {
		out = append(out, natsMessage{message: message})
	}
	return out, nil
}

func (c *natsJetStreamClient) pullSubscription(stream, durable string) (*nats.Subscription, error) {
	key := stream + ":" + durable
	c.mu.Lock()
	defer c.mu.Unlock()
	if sub := c.subs[key]; sub != nil {
		return sub, nil
	}
	sub, err := c.js.PullSubscribe("", durable, nats.Bind(stream, durable))
	if err != nil {
		return nil, err
	}
	c.subs[key] = sub
	return sub, nil
}

func (c *natsJetStreamClient) Close() error {
	if c == nil || c.conn == nil {
		return nil
	}
	c.conn.Close()
	return nil
}

type natsMessage struct {
	message *nats.Msg
}

func (m natsMessage) Data() []byte { return m.message.Data }
func (m natsMessage) DeliveryAttempt() int {
	metadata, err := m.message.Metadata()
	if err != nil || metadata == nil {
		return 0
	}
	return int(metadata.NumDelivered)
}
func (m natsMessage) Ack() error { return m.message.Ack() }
func (m natsMessage) Nak() error { return m.message.Nak() }
func (m natsMessage) NakWithDelay(delay time.Duration) error {
	return m.message.NakWithDelay(delay)
}
