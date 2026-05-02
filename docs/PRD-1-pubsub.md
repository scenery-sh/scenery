# onlava Pub/Sub

This document describes how onlava pub/sub works today, how to define topics and subscriptions, and what runtime behavior to expect.

onlava pub/sub is currently a local, embedded runtime intended to preserve a compact local development flow while keeping the implementation small and predictable.

## Summary

- Package: `github.com/pbrazdil/onlava/pubsub`
- Runtime transport: embedded NATS JetStream
- Message encoding: JSON
- Delivery guarantee supported in onlava v1: `AtLeastOnce`
- `ExactlyOnce`: declared in the API, rejected at runtime in onlava v1
- Local persistence: yes, JetStream file storage under the onlava cache directory
- Separate broker setup: not required

When your app starts through onlava runtime startup, onlava automatically starts the local pub/sub runtime if the app has any registered topics or subscriptions.

## Developer API

The public API is centered around:

- `pubsub.NewTopic[T](name, config)`
- `pubsub.NewSubscription(topic, name, config)`
- `topic.Publish(ctx, msg)`
- `pubsub.MethodHandler(...)` for service methods

Core types:

```go
type TopicConfig struct {
    DeliveryGuarantee DeliveryGuarantee
    OrderingAttribute string
}

type SubscriptionConfig[T any] struct {
    Handler          func(context.Context, T) error
    MaxConcurrency   int
    AckDeadline      time.Duration
    MessageRetention time.Duration
    RetryPolicy      *RetryPolicy
}

type RetryPolicy struct {
    MinBackoff time.Duration
    MaxBackoff time.Duration
    MaxRetries int
}
```

## Basic Example

```go
package emails

import (
    "context"

    "github.com/pbrazdil/onlava/pubsub"
)

type WelcomeEmail struct {
    UserID string `json:"user_id"`
    Email  string `json:"email"`
}

var WelcomeEmails = pubsub.NewTopic[*WelcomeEmail]("welcome-emails", pubsub.TopicConfig{
    DeliveryGuarantee: pubsub.AtLeastOnce,
})

var _ = pubsub.NewSubscription(WelcomeEmails, "send-welcome-email", pubsub.SubscriptionConfig[*WelcomeEmail]{
    Handler: func(ctx context.Context, msg *WelcomeEmail) error {
        return sendEmail(ctx, msg.Email)
    },
})
```

Publishing:

```go
_, err := WelcomeEmails.Publish(ctx, &WelcomeEmail{
    UserID: userID,
    Email:  email,
})
```

`Publish` returns a message ID string derived from the JetStream stream name and sequence number.

## Service Method Handlers

If you want a subscription handler implemented as a method on an onlava service struct, use `pubsub.MethodHandler`.

Example:

```go
package billing

import (
    "context"

    "github.com/pbrazdil/onlava/pubsub"
)

//onlava:service
type Service struct{}

type InvoiceCreated struct {
    InvoiceID string `json:"invoice_id"`
}

var InvoiceCreatedTopic = pubsub.NewTopic[*InvoiceCreated]("invoice-created", pubsub.TopicConfig{
    DeliveryGuarantee: pubsub.AtLeastOnce,
})

var _ = pubsub.NewSubscription(InvoiceCreatedTopic, "notify-accounting", pubsub.SubscriptionConfig[*InvoiceCreated]{
    Handler: pubsub.MethodHandler((*Service).HandleInvoiceCreated),
})

func (s *Service) HandleInvoiceCreated(ctx context.Context, msg *InvoiceCreated) error {
    return nil
}
```

onlava codegen automatically registers a service accessor for `//onlava:service` structs, so method handlers can resolve the initialized service instance at runtime.

If you use `pubsub.MethodHandler` outside that generated onlava service flow, you must register the accessor yourself with:

```go
pubsub.RegisterServiceAccessorFor[*MyService](func() (any, error) {
    return myServiceInstance, nil
})
```

## Message Types

Messages are JSON-encoded. In practice that means:

- your message type `T` must be JSON-marshallable
- exported struct fields are what matter
- standard `json` tags work as expected
- pointer and value message types are both supported

Example:

```go
type Event struct {
    ID      string    `json:"id"`
    Kind    string    `json:"kind"`
    Created time.Time `json:"created"`
}
```

Because the wire format is JSON, onlava pub/sub does not currently use protobuf for message transport.

## Delivery Semantics

onlava v1 supports:

- `pubsub.AtLeastOnce`

onlava v1 does not support:

- `pubsub.ExactlyOnce`

If a topic is declared with `ExactlyOnce`, app startup fails with a clear runtime error.

At-least-once means handlers must be idempotent. A message can be delivered more than once when:

- the handler returns an error
- the handler panics
- the ack deadline is exceeded
- the process is interrupted during delivery

Good practice:

- make handlers safe to retry
- use application-level deduplication when side effects matter
- avoid assuming exactly-once processing

## Subscription Retries

Retries are driven by `RetryPolicy`.

Default retry behavior when `RetryPolicy` is omitted:

- `MinBackoff`: `10s`
- `MaxBackoff`: `10m`
- `MaxRetries`: `100`

Backoff is exponential with a cap at `MaxBackoff`.

Special retry constants:

- `pubsub.NoRetries` (`-2`): immediately dead-letter on first handler failure
- `pubsub.InfiniteRetries` (`-1`): retry forever

Semantics of `MaxRetries`:

- `InfiniteRetries`: never dead-letter due to retry count
- `NoRetries`: dead-letter immediately on failure
- `0`: treated as the default fallback path and dead-letters after more than 100 deliveries
- positive `N`: dead-letter after more than `N+1` deliveries total

That last point matters: the implementation counts deliveries, not just retries.

## Dead-Letter Behavior

When a message can no longer be processed, onlava writes it to a DLQ stream in JetStream.

Dead-lettered payloads include:

- topic name
- subscription name
- error string
- delivery count
- original JSON payload
- timestamp

Current DLQ retention is `7d`.

At the moment, onlava exposes this as internal broker state rather than a polished developer-facing DLQ API. The important practical point is that failed messages are not silently discarded once retry policy is exhausted.

## Ack Deadline

`AckDeadline` controls how long the handler has before the message is considered unacked.

Defaults:

- default: `30s`
- minimum enforced by runtime: `1s`

If the handler runs longer than the ack deadline and does not finish successfully in time, the message may be redelivered.

Choose `AckDeadline` based on realistic handler duration, not ideal duration.

## Message Retention

`MessageRetention` controls how long the underlying topic stream retains data.

Defaults:

- default topic retention: `7d`
- effective stream retention for a topic: the maximum retention requested by any subscription on that topic

This means if one subscription asks for a longer retention window, the topic stream is updated to keep messages that long.

## Concurrency

`MaxConcurrency` limits how many handler goroutines onlava runs concurrently for a specific subscription.

Behavior:

- `MaxConcurrency > 0`: onlava enforces that maximum in the app process
- `MaxConcurrency <= 0`: no application-level semaphore is applied

Important detail:

- even without `MaxConcurrency`, the JetStream consumer is created with `MaxAckPending=1024`
- under load, many messages can therefore be in flight concurrently

If ordering or serialized side effects matter, set `MaxConcurrency: 1`.

## Topic Names and Subscription Names

Names must be non-empty and unique:

- duplicate topic names panic at initialization time
- duplicate subscription names on the same topic panic at initialization time

Internally, onlava sanitizes names for stream and subject usage, but you should still choose stable, human-readable names.

Recommended style:

- topic names: `"user-created"`, `"invoice-created"`
- subscription names: `"send-welcome-email"`, `"sync-search-index"`

## Ordering

`TopicConfig.OrderingAttribute` exists in the public API, but onlava v1 does not currently enforce ordered delivery based on it.

Treat it as reserved for future compatibility for now.

If you need stable ordering today:

- keep `MaxConcurrency: 1`
- design the handler to tolerate retries and occasional reordering

Do not assume `OrderingAttribute` is active until onlava explicitly documents support for it.

## Storage and Persistence

onlava stores JetStream data on disk.

By default the local store lives under:

- `$ONLAVA_DEV_CACHE_DIR/pubsub/<app>`

If `ONLAVA_DEV_CACHE_DIR` is not set, onlava uses the OS user cache directory and stores data under:

- `<user-cache>/onlava/pubsub/<app>`

On macOS that typically resolves under:

- `~/Library/Caches/onlava/pubsub/<app>`

This persistence means messages and stream state can survive process restarts during development.

## Startup and Shutdown

The local pub/sub runtime:

- starts automatically when the app starts, if any topics or subscriptions are declared
- shuts down as part of app shutdown
- drains the NATS connection on shutdown

Only one local pub/sub runtime instance is started per app process.

If the runtime is already started, onlava returns an error instead of silently starting a second copy.

## Error Handling

Handler behavior:

- return `nil`: message is acked
- return error: message is retried or dead-lettered
- panic: panic is recovered, treated like handler failure, then retried or dead-lettered
- JSON decode failure: message is dead-lettered immediately and acked off the main stream

Design guidance:

- return meaningful errors
- keep handlers idempotent
- avoid long blocking operations without an appropriate ack deadline
- prefer explicit timeouts for downstream calls

## Build and Runtime Scope

Current onlava pub/sub is a local runtime feature. It is designed for:

- `onlava run`
- locally run onlava-built binaries

It is not yet documented as a distributed or cloud-managed pub/sub platform.

The current implementation keeps things simple:

- embedded broker
- single app process
- JSON payloads
- local persistence

That is intentional for onlava v1.

## Common Caveats

### `Publish` before runtime startup

If you try to publish before the pub/sub runtime is started, `Publish` returns:

- `pubsub: runtime not started`

### Exactly once is not available

Do not configure:

```go
DeliveryGuarantee: pubsub.ExactlyOnce
```

onlava v1 rejects it on startup.

### Ordering is not active yet

Do not rely on:

```go
OrderingAttribute: "user_id"
```

for actual ordering behavior yet.

### Unlimited concurrency is not truly unlimited, but it is still high

If you omit `MaxConcurrency`, handler execution can still fan out heavily under load. Set it explicitly when side effects or resource use matter.

## Recommended Patterns

### 1. Use small message schemas

Prefer event payloads that contain identifiers and essential fields, not huge snapshots.

### 2. Make handlers idempotent

Assume duplicate delivery is normal.

### 3. Set `MaxConcurrency` explicitly

Do not leave concurrency behavior implicit for subscriptions that touch external systems.

### 4. Tune `AckDeadline`

Set it to cover realistic handler time, including downstream latency.

### 5. Keep topic names stable

Treat topic names as part of the app’s contract.

## Example With Practical Defaults

```go
package users

import (
    "context"
    "time"

    "github.com/pbrazdil/onlava/pubsub"
)

type UserCreated struct {
    UserID string `json:"user_id"`
    Email  string `json:"email"`
}

var UserCreatedTopic = pubsub.NewTopic[*UserCreated]("user-created", pubsub.TopicConfig{
    DeliveryGuarantee: pubsub.AtLeastOnce,
})

var _ = pubsub.NewSubscription(UserCreatedTopic, "send-welcome-email", pubsub.SubscriptionConfig[*UserCreated]{
    MaxConcurrency:   8,
    AckDeadline:      2 * time.Minute,
    MessageRetention: 7 * 24 * time.Hour,
    RetryPolicy: &pubsub.RetryPolicy{
        MinBackoff: 15 * time.Second,
        MaxBackoff: 15 * time.Minute,
        MaxRetries: 20,
    },
    Handler: func(ctx context.Context, msg *UserCreated) error {
        return sendWelcomeEmail(ctx, msg.Email)
    },
})
```

## Current onlava v1 Guarantees

You can rely on:

- local embedded broker startup
- persistent local storage
- JSON message encoding
- at-least-once delivery
- retries with exponential backoff
- dead-lettering after retry exhaustion
- service method handlers via generated accessors for onlava service structs

You should not rely on yet:

- exactly-once delivery
- ordering by `OrderingAttribute`
- protobuf wire format
- remote broker hosting or cloud-managed orchestration
