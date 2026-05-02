package pubsub

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	nserver "github.com/nats-io/nats-server/v2/server"
	"github.com/nats-io/nats.go"
	onlavaruntime "github.com/pbrazdil/onlava/runtime"
)

const (
	NoRetries       = -2
	InfiniteRetries = -1
)

type DeliveryGuarantee int

const (
	AtLeastOnce DeliveryGuarantee = iota + 1
	ExactlyOnce
)

type TopicConfig struct {
	DeliveryGuarantee DeliveryGuarantee
	OrderingAttribute string
}

type RetryPolicy struct {
	MinBackoff time.Duration
	MaxBackoff time.Duration
	MaxRetries int
}

type TopicMeta struct {
	Name   string
	Config TopicConfig
}

type TopicPerms[T any] interface {
	Meta() TopicMeta
}

type Publisher[T any] interface {
	Publish(ctx context.Context, msg T) (id string, err error)
	Meta() TopicMeta
}

type Topic[T any] struct {
	decl *topicDecl
}

type SubscriptionConfig[T any] struct {
	Handler          func(ctx context.Context, msg T) error
	MaxConcurrency   int
	AckDeadline      time.Duration
	MessageRetention time.Duration
	RetryPolicy      *RetryPolicy
}

type SubscriptionMeta[T any] struct {
	Name   string
	Config SubscriptionConfig[T]
	Topic  TopicMeta
}

type Subscription[T any] struct {
	decl *subscriptionDecl
	meta SubscriptionMeta[T]
	cfg  SubscriptionConfig[T]
}

type LocalRuntimeConfig struct {
	AppID string
}

type dlqMessage struct {
	Topic        string          `json:"topic"`
	Subscription string          `json:"subscription"`
	Error        string          `json:"error"`
	Deliveries   int             `json:"deliveries"`
	Payload      json.RawMessage `json:"payload"`
	CreatedAt    time.Time       `json:"created_at"`
}

type topicDecl struct {
	name    string
	cfg     TopicConfig
	msgType reflect.Type
}

type subscriptionDecl struct {
	topic       *topicDecl
	name        string
	msgType     reflect.Type
	handler     func(context.Context, any) error
	cfgAny      any
	serviceName string
	maxConc     int
	ackDeadline time.Duration
	retention   time.Duration
	retry       RetryPolicy
}

type localRuntime struct {
	appID      string
	server     *nserver.Server
	conn       *nats.Conn
	js         nats.JetStreamContext
	topics     map[*topicDecl]runtimeTopic
	stats      map[string]*subscriptionStats
	published  map[string]*atomic.Int64
	dlqStream  string
	dlqSubject string
	cancel     context.CancelFunc
	wg         sync.WaitGroup
}

type runtimeTopic struct {
	stream  string
	subject string
}

type subscriptionStats struct {
	topic        string
	subscription string
	stream       string
	durable      string
	workers      int
	pickedUp     atomic.Int64
	completed    atomic.Int64
	failed       atomic.Int64
	deadLettered atomic.Int64
	inFlight     atomic.Int64
	totalNanos   atomic.Int64
}

type registry struct {
	mu               sync.RWMutex
	topics           map[string]*topicDecl
	subscriptions    map[string]*subscriptionDecl
	serviceAccessors map[string]func() (any, error)
	runtime          *localRuntime
}

var global = &registry{
	topics:           make(map[string]*topicDecl),
	subscriptions:    make(map[string]*subscriptionDecl),
	serviceAccessors: make(map[string]func() (any, error)),
}

func NewTopic[T any](name string, cfg TopicConfig) *Topic[T] {
	name = strings.TrimSpace(name)
	if name == "" {
		panic("pubsub: topic name must not be empty")
	}
	decl := &topicDecl{
		name:    name,
		cfg:     cfg,
		msgType: reflect.TypeFor[T](),
	}
	global.mu.Lock()
	defer global.mu.Unlock()
	if _, exists := global.topics[name]; exists {
		panic(fmt.Sprintf("pubsub: duplicate topic %q", name))
	}
	global.topics[name] = decl
	return &Topic[T]{decl: decl}
}

func (t *Topic[T]) Meta() TopicMeta {
	if t == nil || t.decl == nil {
		return TopicMeta{}
	}
	return TopicMeta{Name: t.decl.name, Config: t.decl.cfg}
}

func (t *Topic[T]) Publish(ctx context.Context, msg T) (string, error) {
	if t == nil || t.decl == nil {
		return "", errors.New("pubsub: nil topic")
	}
	global.mu.RLock()
	rt := global.runtime
	global.mu.RUnlock()
	if rt == nil {
		return "", errors.New("pubsub: runtime not started")
	}
	return rt.publish(ctx, t.decl, msg)
}

func TopicRef[P TopicPerms[T], T any](topic *Topic[T]) P {
	ref, ok := any(topic).(P)
	if !ok {
		panic("pubsub: topic does not satisfy requested permissions")
	}
	return ref
}

func NewSubscription[T any](topic *Topic[T], name string, cfg SubscriptionConfig[T]) *Subscription[T] {
	if topic == nil || topic.decl == nil {
		panic("pubsub: subscription topic must not be nil")
	}
	if cfg.Handler == nil {
		panic("pubsub: subscription handler must not be nil")
	}
	name = strings.TrimSpace(name)
	if name == "" {
		panic("pubsub: subscription name must not be empty")
	}
	decl := &subscriptionDecl{
		topic:       topic.decl,
		name:        name,
		msgType:     reflect.TypeFor[T](),
		cfgAny:      cfg,
		serviceName: handlerServiceName(cfg.Handler),
		maxConc:     cfg.MaxConcurrency,
		ackDeadline: normalizeAckDeadline(cfg.AckDeadline),
		retention:   normalizeRetention(cfg.MessageRetention),
		retry:       normalizeRetry(cfg.RetryPolicy),
		handler: func(ctx context.Context, msg any) error {
			typed, ok := msg.(T)
			if !ok {
				return fmt.Errorf("pubsub: unexpected message type %T for %s/%s", msg, topic.decl.name, name)
			}
			return cfg.Handler(ctx, typed)
		},
	}

	key := topic.decl.name + ":" + name
	global.mu.Lock()
	defer global.mu.Unlock()
	if _, exists := global.subscriptions[key]; exists {
		panic(fmt.Sprintf("pubsub: duplicate subscription %q on topic %q", name, topic.decl.name))
	}
	global.subscriptions[key] = decl
	return &Subscription[T]{
		decl: decl,
		meta: SubscriptionMeta[T]{
			Name: name,
			Config: SubscriptionConfig[T]{
				Handler:          cfg.Handler,
				MaxConcurrency:   cfg.MaxConcurrency,
				AckDeadline:      decl.ackDeadline,
				MessageRetention: decl.retention,
				RetryPolicy:      cloneRetryPolicy(cfg.RetryPolicy),
			},
			Topic: topic.Meta(),
		},
		cfg: SubscriptionConfig[T]{
			Handler:          cfg.Handler,
			MaxConcurrency:   cfg.MaxConcurrency,
			AckDeadline:      decl.ackDeadline,
			MessageRetention: decl.retention,
			RetryPolicy:      cloneRetryPolicy(cfg.RetryPolicy),
		},
	}
}

func (s *Subscription[T]) Config() SubscriptionConfig[T] {
	if s == nil {
		return SubscriptionConfig[T]{}
	}
	cfg := s.cfg
	cfg.RetryPolicy = cloneRetryPolicy(cfg.RetryPolicy)
	return cfg
}

func (s *Subscription[T]) Meta() SubscriptionMeta[T] {
	if s == nil {
		return SubscriptionMeta[T]{}
	}
	meta := s.meta
	meta.Config.RetryPolicy = cloneRetryPolicy(meta.Config.RetryPolicy)
	return meta
}

func MethodHandler[T, SvcStruct any](handler func(s SvcStruct, ctx context.Context, msg T) error) func(ctx context.Context, msg T) error {
	serviceKey := serviceKeyForType(reflect.TypeFor[SvcStruct]())
	return func(ctx context.Context, msg T) error {
		global.mu.RLock()
		accessor := global.serviceAccessors[serviceKey]
		global.mu.RUnlock()
		if accessor == nil {
			return fmt.Errorf("pubsub: no service accessor registered for %s", serviceKey)
		}
		svcAny, err := accessor()
		if err != nil {
			return err
		}
		svc, ok := svcAny.(SvcStruct)
		if !ok {
			return fmt.Errorf("pubsub: service accessor returned %T, want %s", svcAny, serviceKey)
		}
		return handler(svc, ctx, msg)
	}
}

func RegisterServiceAccessorFor[T any](getter func() (any, error)) {
	if getter == nil {
		panic("pubsub: service accessor getter must not be nil")
	}
	key := serviceKeyForType(reflect.TypeFor[T]())
	global.mu.Lock()
	defer global.mu.Unlock()
	global.serviceAccessors[key] = getter
}

func StartLocalRuntime(ctx context.Context, cfg LocalRuntimeConfig) (func(context.Context) error, error) {
	topics, subs, err := snapshotDeclarations()
	if err != nil {
		return nil, err
	}
	if len(topics) == 0 && len(subs) == 0 {
		return func(context.Context) error { return nil }, nil
	}
	if strings.TrimSpace(cfg.AppID) == "" {
		return nil, errors.New("pubsub: app id must not be empty")
	}
	for _, topic := range topics {
		if topic.cfg.DeliveryGuarantee == ExactlyOnce {
			return nil, fmt.Errorf("pubsub: topic %q uses ExactlyOnce, which is not supported in onlava v1", topic.name)
		}
	}

	global.mu.Lock()
	if global.runtime != nil {
		global.mu.Unlock()
		return nil, errors.New("pubsub: runtime already started")
	}
	global.mu.Unlock()

	storeDir, err := localStoreDir(cfg.AppID)
	if err != nil {
		return nil, err
	}
	opts := &nserver.Options{
		ServerName:      "onlava-pubsub-" + sanitizeName(cfg.AppID),
		Host:            "127.0.0.1",
		Port:            -1,
		JetStream:       true,
		StoreDir:        storeDir,
		NoSigs:          true,
		NoLog:           true,
		NoSystemAccount: true,
	}
	srv, err := nserver.NewServer(opts)
	if err != nil {
		return nil, err
	}
	go srv.Start()
	if !srv.ReadyForConnections(10 * time.Second) {
		srv.Shutdown()
		return nil, errors.New("pubsub: embedded NATS server failed to start")
	}

	conn, err := nats.Connect(srv.ClientURL(), nats.Name("onlava pubsub"))
	if err != nil {
		srv.Shutdown()
		return nil, err
	}
	js, err := conn.JetStream()
	if err != nil {
		conn.Close()
		srv.Shutdown()
		return nil, err
	}

	runCtx, cancel := context.WithCancel(ctx)
	rt := &localRuntime{
		appID:      cfg.AppID,
		server:     srv,
		conn:       conn,
		js:         js,
		topics:     make(map[*topicDecl]runtimeTopic, len(topics)),
		stats:      make(map[string]*subscriptionStats, len(subs)),
		published:  make(map[string]*atomic.Int64, len(topics)),
		dlqStream:  "ONLAVA_DLQ_" + sanitizeName(cfg.AppID),
		dlqSubject: "onlava." + sanitizeSubjectPart(cfg.AppID) + ".dlq.>",
		cancel:     cancel,
	}
	if err := rt.ensureDLQStream(); err != nil {
		cancel()
		conn.Close()
		srv.Shutdown()
		return nil, err
	}
	for _, topic := range topics {
		rTopic, err := rt.ensureTopic(topic, subs)
		if err != nil {
			cancel()
			conn.Close()
			srv.Shutdown()
			return nil, err
		}
		rt.topics[topic] = rTopic
		rt.published[topic.name] = &atomic.Int64{}
	}
	if _, err := rt.clearAll(runCtx); err != nil {
		cancel()
		conn.Close()
		srv.Shutdown()
		return nil, err
	}
	for _, sub := range subs {
		if err := rt.startSubscription(runCtx, sub); err != nil {
			cancel()
			rt.wait()
			conn.Close()
			srv.Shutdown()
			return nil, err
		}
	}

	global.mu.Lock()
	global.runtime = rt
	global.mu.Unlock()
	rt.reportPubSubSnapshot()
	rt.wg.Add(1)
	go rt.reportPubSubSnapshots(runCtx)

	return func(stopCtx context.Context) error {
		global.mu.Lock()
		if global.runtime == rt {
			global.runtime = nil
		}
		global.mu.Unlock()
		rt.cancel()
		done := make(chan struct{})
		go func() {
			rt.wait()
			close(done)
		}()
		select {
		case <-done:
		case <-stopCtx.Done():
			return stopCtx.Err()
		}
		if err := rt.conn.Drain(); err != nil && !errors.Is(err, nats.ErrConnectionClosed) {
			rt.conn.Close()
			rt.server.Shutdown()
			return err
		}
		rt.conn.Close()
		rt.server.Shutdown()
		return nil
	}, nil
}

func snapshotDeclarations() ([]*topicDecl, []*subscriptionDecl, error) {
	global.mu.RLock()
	defer global.mu.RUnlock()
	topics := make([]*topicDecl, 0, len(global.topics))
	for _, topic := range global.topics {
		topics = append(topics, topic)
	}
	subs := make([]*subscriptionDecl, 0, len(global.subscriptions))
	for _, sub := range global.subscriptions {
		subs = append(subs, sub)
	}
	sort.Slice(topics, func(i, j int) bool { return topics[i].name < topics[j].name })
	sort.Slice(subs, func(i, j int) bool {
		if subs[i].topic.name == subs[j].topic.name {
			return subs[i].name < subs[j].name
		}
		return subs[i].topic.name < subs[j].topic.name
	})
	for _, sub := range subs {
		if _, ok := global.topics[sub.topic.name]; !ok {
			return nil, nil, fmt.Errorf("pubsub: subscription %q references unknown topic %q", sub.name, sub.topic.name)
		}
	}
	return topics, subs, nil
}

func (rt *localRuntime) ensureTopic(topic *topicDecl, subs []*subscriptionDecl) (runtimeTopic, error) {
	streamName := "ONLAVA_" + sanitizeName(rt.appID) + "_" + sanitizeName(topic.name)
	subject := "onlava." + sanitizeSubjectPart(rt.appID) + "." + sanitizeSubjectPart(topic.name)
	maxAge := defaultMessageRetention
	for _, sub := range subs {
		if sub.topic == topic && sub.retention > maxAge {
			maxAge = sub.retention
		}
	}
	_, err := rt.js.AddStream(&nats.StreamConfig{
		Name:      streamName,
		Subjects:  []string{subject},
		Storage:   nats.FileStorage,
		Retention: nats.LimitsPolicy,
		MaxAge:    maxAge,
		Discard:   nats.DiscardOld,
	})
	if err != nil && !isAlreadyExistsError(err) {
		return runtimeTopic{}, err
	}
	if err == nil {
		return runtimeTopic{stream: streamName, subject: subject}, nil
	}
	if _, err := rt.js.UpdateStream(&nats.StreamConfig{
		Name:      streamName,
		Subjects:  []string{subject},
		Storage:   nats.FileStorage,
		Retention: nats.LimitsPolicy,
		MaxAge:    maxAge,
		Discard:   nats.DiscardOld,
	}); err != nil {
		return runtimeTopic{}, err
	}
	return runtimeTopic{stream: streamName, subject: subject}, nil
}

func (rt *localRuntime) ensureDLQStream() error {
	_, err := rt.js.AddStream(&nats.StreamConfig{
		Name:      rt.dlqStream,
		Subjects:  []string{rt.dlqSubject},
		Storage:   nats.FileStorage,
		Retention: nats.LimitsPolicy,
		MaxAge:    7 * 24 * time.Hour,
		Discard:   nats.DiscardOld,
	})
	if err != nil && !isAlreadyExistsError(err) {
		return err
	}
	if err == nil {
		return nil
	}
	_, err = rt.js.UpdateStream(&nats.StreamConfig{
		Name:      rt.dlqStream,
		Subjects:  []string{rt.dlqSubject},
		Storage:   nats.FileStorage,
		Retention: nats.LimitsPolicy,
		MaxAge:    7 * 24 * time.Hour,
		Discard:   nats.DiscardOld,
	})
	return err
}

func (rt *localRuntime) publish(ctx context.Context, topic *topicDecl, msg any) (string, error) {
	rTopic, ok := rt.topics[topic]
	if !ok {
		return "", fmt.Errorf("pubsub: topic %q not initialized", topic.name)
	}
	insertedAt := time.Now().UTC()
	data, err := json.Marshal(msg)
	if err != nil {
		return "", err
	}
	ack, err := rt.js.PublishMsg(&nats.Msg{
		Subject: rTopic.subject,
		Data:    data,
	}, nats.Context(ctx))
	if err != nil {
		return "", err
	}
	if counter := rt.published[topic.name]; counter != nil {
		counter.Add(1)
	}
	messageID := fmt.Sprintf("%s:%d", ack.Stream, ack.Sequence)
	rt.reportQueuedMessages(topic, messageID, data, insertedAt)
	rt.reportPubSubSnapshot()
	return messageID, nil
}

func ClearLocalRuntime(ctx context.Context) ([]map[string]any, error) {
	global.mu.RLock()
	rt := global.runtime
	global.mu.RUnlock()
	if rt == nil {
		return []map[string]any{}, nil
	}
	return rt.clearAll(ctx)
}

func (rt *localRuntime) clearAll(ctx context.Context) ([]map[string]any, error) {
	streams := make(map[string]struct{}, len(rt.topics))
	for _, topic := range rt.topics {
		streams[topic.stream] = struct{}{}
	}
	if rt.dlqStream != "" {
		streams[rt.dlqStream] = struct{}{}
	}
	names := make([]string, 0, len(streams))
	for stream := range streams {
		names = append(names, stream)
	}
	sort.Strings(names)
	for _, stream := range names {
		if err := rt.js.PurgeStream(stream, nats.Context(ctx)); err != nil {
			return nil, fmt.Errorf("pubsub: clear stream %s: %w", stream, err)
		}
	}
	rt.reportPubSubSnapshot()
	return rt.pubSubSnapshot(), nil
}

func (rt *localRuntime) startSubscription(ctx context.Context, sub *subscriptionDecl) error {
	rTopic, ok := rt.topics[sub.topic]
	if !ok {
		return fmt.Errorf("pubsub: topic %q not initialized for subscription %q", sub.topic.name, sub.name)
	}
	durable := "ONLAVA_" + sanitizeName(rt.appID) + "_" + sanitizeName(sub.topic.name) + "_" + sanitizeName(sub.name)
	maxAckPending := 1024
	if sub.maxConc > 0 {
		maxAckPending = sub.maxConc
	}
	if err := rt.ensureConsumerConfig(rTopic.stream, durable, sub, maxAckPending); err != nil {
		return err
	}
	msgCh := make(chan *nats.Msg, max(maxAckPending, 64))
	jsSub, err := rt.js.ChanSubscribe(
		rTopic.subject,
		msgCh,
		nats.BindStream(rTopic.stream),
		nats.Durable(durable),
		nats.ManualAck(),
		nats.AckWait(sub.ackDeadline),
		nats.MaxAckPending(maxAckPending),
		nats.DeliverAll(),
		nats.MaxDeliver(-1),
	)
	if err != nil {
		return err
	}
	stats := rt.subscriptionStats(sub, rTopic.stream, durable, sub.maxConc)

	var sem chan struct{}
	if sub.maxConc > 0 {
		sem = make(chan struct{}, sub.maxConc)
	}
	rt.wg.Add(1)
	go func() {
		defer rt.wg.Done()
		defer func() {
			_ = jsSub.Unsubscribe()
		}()
		for {
			select {
			case <-ctx.Done():
				return
			case msg, ok := <-msgCh:
				if !ok {
					return
				}
				if msg == nil {
					continue
				}
				if sem != nil {
					select {
					case sem <- struct{}{}:
					case <-ctx.Done():
						return
					}
				}
				rt.wg.Add(1)
				go func(msg *nats.Msg) {
					defer rt.wg.Done()
					defer func() {
						if sem != nil {
							<-sem
						}
					}()
					rt.handleMessage(ctx, sub, msg, stats)
				}(msg)
			}
		}
	}()
	return nil
}

func (rt *localRuntime) ensureConsumerConfig(stream, durable string, sub *subscriptionDecl, maxAckPending int) error {
	info, err := rt.js.ConsumerInfo(stream, durable)
	if err != nil {
		if isNotFoundError(err) {
			return nil
		}
		return err
	}
	if info == nil {
		return nil
	}
	cfg := info.Config
	if cfg.MaxAckPending == maxAckPending && cfg.AckWait == sub.ackDeadline && cfg.MaxDeliver == -1 {
		return nil
	}
	cfg.MaxAckPending = maxAckPending
	cfg.AckWait = sub.ackDeadline
	cfg.MaxDeliver = -1
	if _, err := rt.js.UpdateConsumer(stream, &cfg); err != nil {
		return fmt.Errorf("pubsub: update consumer %s/%s after config change: %w", stream, durable, err)
	}
	return nil
}

func (rt *localRuntime) handleMessage(parent context.Context, sub *subscriptionDecl, msg *nats.Msg, stats *subscriptionStats) {
	meta, _ := msg.Metadata()
	handlerCtx, cancel := context.WithTimeout(parent, sub.ackDeadline)
	defer cancel()
	started := time.Now()
	messageID := messageIDFromMetadata(meta, sub.topic, msg)
	attempt := max(1, metaDeliveries(meta))
	insertedAt := started
	if meta != nil && !meta.Timestamp.IsZero() {
		insertedAt = meta.Timestamp.UTC()
	}
	trace := onlavaruntime.StartPubSubMessageTrace(firstNonEmpty(sub.serviceName, sub.topic.name), sub.name, sub.topic.name, messageID, msg.Data)
	traceID := ""
	if trace != nil {
		traceID = trace.TraceID
	}
	rt.reportMessage(sub, messageID, msg.Data, "processing", traceID, attempt, started, insertedAt, time.Time{}, 0, nil, metaDeliveries(meta))
	if stats != nil {
		stats.pickedUp.Add(1)
		stats.inFlight.Add(1)
		defer func() {
			stats.inFlight.Add(-1)
			stats.totalNanos.Add(time.Since(started).Nanoseconds())
			rt.reportPubSubSnapshot()
		}()
	}
	var payload any
	if err := decodeMessage(msg.Data, sub.msgType, &payload); err != nil {
		_ = rt.publishDLQ(sub, msg.Data, metaDeliveries(meta), err)
		_ = msg.Ack()
		if stats != nil {
			stats.failed.Add(1)
			stats.deadLettered.Add(1)
		}
		duration := time.Since(started)
		onlavaruntime.FinishPubSubMessageTrace(trace, firstNonEmpty(sub.serviceName, sub.topic.name), sub.name, sub.topic.name, messageID, duration, err)
		rt.reportMessage(sub, messageID, msg.Data, "dead_lettered", traceID, attempt, started, insertedAt, time.Now().UTC(), duration, err, metaDeliveries(meta))
		return
	}
	err := invokeHandler(handlerCtx, sub.handler, payload)
	if err == nil {
		_ = msg.Ack()
		if stats != nil {
			stats.completed.Add(1)
		}
		duration := time.Since(started)
		onlavaruntime.FinishPubSubMessageTrace(trace, firstNonEmpty(sub.serviceName, sub.topic.name), sub.name, sub.topic.name, messageID, duration, nil)
		rt.reportMessage(sub, messageID, msg.Data, "completed", traceID, attempt, started, insertedAt, time.Now().UTC(), duration, nil, metaDeliveries(meta))
		return
	}
	if stats != nil {
		stats.failed.Add(1)
	}
	deliveries := metaDeliveries(meta)
	if shouldDeadLetter(sub.retry.MaxRetries, deliveries) {
		_ = rt.publishDLQ(sub, msg.Data, deliveries, err)
		_ = msg.Ack()
		if stats != nil {
			stats.deadLettered.Add(1)
		}
		duration := time.Since(started)
		onlavaruntime.FinishPubSubMessageTrace(trace, firstNonEmpty(sub.serviceName, sub.topic.name), sub.name, sub.topic.name, messageID, duration, err)
		rt.reportMessage(sub, messageID, msg.Data, "dead_lettered", traceID, attempt, started, insertedAt, time.Now().UTC(), duration, err, deliveries)
		return
	}
	delay := retryDelay(sub.retry, deliveries)
	if delay > 0 {
		_ = msg.NakWithDelay(delay)
		duration := time.Since(started)
		onlavaruntime.FinishPubSubMessageTrace(trace, firstNonEmpty(sub.serviceName, sub.topic.name), sub.name, sub.topic.name, messageID, duration, err)
		rt.reportMessage(sub, messageID, msg.Data, "retrying", traceID, attempt, started, insertedAt, time.Now().UTC(), duration, err, deliveries)
		return
	}
	_ = msg.Nak()
	duration := time.Since(started)
	onlavaruntime.FinishPubSubMessageTrace(trace, firstNonEmpty(sub.serviceName, sub.topic.name), sub.name, sub.topic.name, messageID, duration, err)
	rt.reportMessage(sub, messageID, msg.Data, "retrying", traceID, attempt, started, insertedAt, time.Now().UTC(), duration, err, deliveries)
}

func (rt *localRuntime) publishDLQ(sub *subscriptionDecl, payload []byte, deliveries int, err error) error {
	body, marshalErr := json.Marshal(dlqMessage{
		Topic:        sub.topic.name,
		Subscription: sub.name,
		Error:        err.Error(),
		Deliveries:   deliveries,
		Payload:      append([]byte(nil), payload...),
		CreatedAt:    time.Now().UTC(),
	})
	if marshalErr != nil {
		return marshalErr
	}
	subject := "onlava." + sanitizeSubjectPart(rt.appID) + ".dlq." + sanitizeSubjectPart(sub.topic.name) + "." + sanitizeSubjectPart(sub.name)
	_, pubErr := rt.js.Publish(subject, body)
	return pubErr
}

func (rt *localRuntime) wait() {
	rt.wg.Wait()
}

func (rt *localRuntime) subscriptionStats(sub *subscriptionDecl, stream, durable string, workers int) *subscriptionStats {
	key := sub.topic.name + ":" + sub.name
	if existing := rt.stats[key]; existing != nil {
		return existing
	}
	stats := &subscriptionStats{
		topic:        sub.topic.name,
		subscription: sub.name,
		stream:       stream,
		durable:      durable,
		workers:      workers,
	}
	rt.stats[key] = stats
	return stats
}
