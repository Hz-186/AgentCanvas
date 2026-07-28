package queue

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"agentcanvas/internal/domain/reflection"
	"agentcanvas/internal/observability"
	"agentcanvas/internal/pkg/config"

	"github.com/nats-io/nats.go"
)

type ReflectionNATSTransport struct {
	conn           *nats.Conn
	js             nats.JetStreamContext
	cfg            config.ReflectionQueueConfig
	log            *slog.Logger
	mu             sync.Mutex
	sub            *nats.Subscription
	resourceMu     sync.Mutex
	resourcesReady bool
}

func NewReflectionNATSTransport(natsCfg config.NATSConfig, cfg config.ReflectionQueueConfig, logger *slog.Logger) (*ReflectionNATSTransport, error) {
	if logger == nil {
		logger = slog.Default()
	}
	options := []nats.Option{
		nats.Name("agentcanvas-reflection-worker"),
		nats.MaxReconnects(-1),
		nats.ReconnectWait(time.Duration(natsCfg.ReconnectWaitSeconds) * time.Second),
		nats.RetryOnFailedConnect(true),
		nats.Timeout(5 * time.Second),
		nats.DisconnectErrHandler(func(_ *nats.Conn, err error) {
			logger.Warn("reflection nats disconnected", "error", err)
		}),
		nats.ReconnectHandler(func(conn *nats.Conn) {
			logger.Info("reflection nats reconnected", "url", conn.ConnectedUrl())
		}),
		nats.ClosedHandler(func(_ *nats.Conn) {
			logger.Warn("reflection nats connection closed")
		}),
	}
	if strings.TrimSpace(natsCfg.CredentialsFile) != "" {
		options = append(options, nats.UserCredentials(natsCfg.CredentialsFile))
	}
	if strings.TrimSpace(natsCfg.TLSCAFile) != "" {
		options = append(options, nats.RootCAs(natsCfg.TLSCAFile))
	}
	if strings.TrimSpace(natsCfg.TLSCertFile) != "" || strings.TrimSpace(natsCfg.TLSKeyFile) != "" {
		if natsCfg.TLSCertFile == "" || natsCfg.TLSKeyFile == "" {
			return nil, fmt.Errorf("both nats tls_cert_file and tls_key_file are required")
		}
		options = append(options, nats.ClientCert(natsCfg.TLSCertFile, natsCfg.TLSKeyFile))
	}
	conn, err := nats.Connect(natsCfg.URL, options...)
	if err != nil {
		return nil, err
	}
	js, err := conn.JetStream()
	if err != nil {
		conn.Close()
		return nil, err
	}
	return &ReflectionNATSTransport{conn: conn, js: js, cfg: cfg, log: logger}, nil
}

func (t *ReflectionNATSTransport) ensureResources() error {
	t.resourceMu.Lock()
	defer t.resourceMu.Unlock()
	if t.resourcesReady {
		return nil
	}
	maxAge := time.Duration(t.cfg.StreamMaxAgeDays) * 24 * time.Hour
	jobs := &nats.StreamConfig{Name: t.cfg.Stream, Subjects: []string{t.cfg.Subject}, Retention: nats.WorkQueuePolicy,
		Storage: nats.FileStorage, MaxAge: maxAge, MaxBytes: t.cfg.StreamMaxBytes, Replicas: t.cfg.StreamReplicas, Duplicates: 10 * time.Minute}
	if err := t.ensureStream(jobs); err != nil {
		return err
	}
	dlq := &nats.StreamConfig{Name: t.cfg.DLQStream, Subjects: []string{t.cfg.DLQSubject}, Retention: nats.LimitsPolicy,
		Storage: nats.FileStorage, MaxAge: maxAge, MaxBytes: t.cfg.StreamMaxBytes, Replicas: t.cfg.StreamReplicas, Duplicates: 10 * time.Minute}
	if err := t.ensureStream(dlq); err != nil {
		return err
	}
	consumer := &nats.ConsumerConfig{Durable: t.cfg.Durable, FilterSubject: t.cfg.Subject, AckPolicy: nats.AckExplicitPolicy,
		AckWait: time.Duration(t.cfg.AckWaitSeconds) * time.Second, DeliverPolicy: nats.DeliverAllPolicy,
		MaxAckPending: t.cfg.MaxAckPending, MaxDeliver: -1, ReplayPolicy: nats.ReplayInstantPolicy}
	if _, err := t.js.ConsumerInfo(t.cfg.Stream, t.cfg.Durable); err == nil {
		if _, err := t.js.UpdateConsumer(t.cfg.Stream, consumer); err != nil {
			return err
		}
	} else {
		if _, err := t.js.AddConsumer(t.cfg.Stream, consumer); err != nil {
			return err
		}
	}
	t.resourcesReady = true
	return nil
}

func (t *ReflectionNATSTransport) ensureStream(expected *nats.StreamConfig) error {
	info, err := t.js.StreamInfo(expected.Name)
	if err != nil {
		_, err = t.js.AddStream(expected)
		return err
	}
	current := info.Config
	current.Subjects = expected.Subjects
	current.Retention = expected.Retention
	current.Storage = expected.Storage
	current.MaxAge = expected.MaxAge
	current.MaxBytes = expected.MaxBytes
	current.Replicas = expected.Replicas
	current.Duplicates = expected.Duplicates
	_, err = t.js.UpdateStream(&current)
	return err
}

func (t *ReflectionNATSTransport) PublishOutbox(ctx context.Context, item reflection.JobOutbox) error {
	if err := t.ensureResources(); err != nil {
		return err
	}
	reason := "created"
	if item.DispatchSeq > 1 {
		reason = "retry"
	}
	envelope := reflection.Envelope{SchemaVersion: 1, EventID: item.EventID, JobID: item.JobID, DispatchSeq: item.DispatchSeq,
		DispatchReason: reason, OccurredAt: item.CreatedAt.UTC()}
	if item.EventType == reflection.OutboxDLQ {
		return t.publishEnvelope(ctx, t.cfg.DLQSubject, envelope, "business attempts exhausted")
	}
	return t.publishEnvelope(ctx, t.cfg.Subject, envelope, "")
}

func (t *ReflectionNATSTransport) publishEnvelope(ctx context.Context, subject string, envelope reflection.Envelope, reason string) error {
	payload := struct {
		reflection.Envelope
		Reason string `json:"reason,omitempty"`
	}{Envelope: envelope, Reason: reason}
	data, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	msg := &nats.Msg{Subject: subject, Data: data, Header: nats.Header{}}
	msg.Header.Set(nats.MsgIdHdr, envelope.EventID)
	msg.Header.Set("Content-Type", "application/json")
	_, err = t.js.PublishMsg(msg, nats.Context(ctx))
	return err
}

func (t *ReflectionNATSTransport) Fetch(ctx context.Context, limit int) ([]reflection.Delivery, error) {
	if err := t.ensureResources(); err != nil {
		return nil, err
	}
	if limit <= 0 {
		limit = 1
	}
	sub, err := t.subscription()
	if err != nil {
		return nil, err
	}
	fetchCtx, cancel := context.WithTimeout(ctx, time.Second)
	defer cancel()
	messages, err := sub.Fetch(limit, nats.Context(fetchCtx))
	if err != nil {
		if errors.Is(err, nats.ErrTimeout) || errors.Is(err, context.DeadlineExceeded) {
			return nil, nil
		}
		return nil, err
	}
	result := make([]reflection.Delivery, 0, len(messages))
	for _, message := range messages {
		if metadata, metadataErr := message.Metadata(); metadataErr == nil && metadata.NumDelivered > 1 {
			observability.ReflectionSystemMetrics.RecordRedelivery()
		}
		var envelope reflection.Envelope
		validationErr := json.Unmarshal(message.Data, &envelope)
		if validationErr == nil && (envelope.SchemaVersion != 1 || envelope.EventID == "" || envelope.JobID <= 0) {
			validationErr = fmt.Errorf("invalid reflection envelope")
		}
		result = append(result, &reflectionNATSDelivery{message: message, envelope: envelope, validationErr: validationErr})
	}
	return result, nil
}

func (t *ReflectionNATSTransport) subscription() (*nats.Subscription, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.sub != nil && t.sub.IsValid() {
		return t.sub, nil
	}
	sub, err := t.js.PullSubscribe(t.cfg.Subject, t.cfg.Durable, nats.Bind(t.cfg.Stream, t.cfg.Durable))
	if err != nil {
		return nil, err
	}
	t.sub = sub
	return sub, nil
}

func (t *ReflectionNATSTransport) PublishDLQ(ctx context.Context, envelope reflection.Envelope, reason string) error {
	if err := t.ensureResources(); err != nil {
		return err
	}
	if envelope.EventID == "" {
		envelope.EventID = fmt.Sprintf("reflection:poison:%d", time.Now().UnixNano())
	}
	return t.publishEnvelope(ctx, t.cfg.DLQSubject, envelope, reason)
}

func (t *ReflectionNATSTransport) Drain() error {
	if t == nil || t.conn == nil {
		return nil
	}
	return t.conn.Drain()
}

type reflectionNATSDelivery struct {
	message       *nats.Msg
	envelope      reflection.Envelope
	validationErr error
}

func (d *reflectionNATSDelivery) Envelope() reflection.Envelope { return d.envelope }
func (d *reflectionNATSDelivery) ValidationError() error        { return d.validationErr }
func (d *reflectionNATSDelivery) Ack(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	return d.message.Ack()
}
func (d *reflectionNATSDelivery) Nak(ctx context.Context, delay time.Duration) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if delay > 0 {
		return d.message.NakWithDelay(delay)
	}
	return d.message.Nak()
}
func (d *reflectionNATSDelivery) InProgress(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	return d.message.InProgress()
}
func (d *reflectionNATSDelivery) Term(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	return d.message.Term()
}

var _ reflection.Transport = (*ReflectionNATSTransport)(nil)
