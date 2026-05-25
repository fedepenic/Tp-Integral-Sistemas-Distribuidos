# Worker Abstractions

This package provides a reusable, stateful aggregator worker with:

- Distributed EOF propagation using a control exchange.
- Pending-message tracking to prevent premature flush.
- Pluggable aggregation logic (no RabbitMQ knowledge in business logic).

## AggregatorWorker

The worker consumes `protocol.Batch` messages from a partitioned exchange/queue
and emits `ResultBatch` messages to an output queue.

### Required config

- `InstanceID`
- `ConnSettings` (RabbitMQ host/port)
- `InputExchange` (direct exchange for partitioned input)
- `InputKey` (routing key for this instance)
- `OutputQueue` (or `OutputExchange`)
- `ControlExchange`
- `ControlKey`
- `UpstreamInstances` (expected EOFs per client/task)

### Optional output routing

- `OutputExchange` enables publishing to a direct exchange instead of a queue.
- `OutputKey` is the fixed routing key used when sending EOFs or when the
  aggregator does not provide a result routing function.
- When a `ResultKeyFunc` is provided, results are grouped by key and published
  with `SendWithKey` for each group. If the output is a queue, the key is
  ignored but batches remain separated per key, mirroring `filter-worker`.

### Routing notes

- The input exchange must be `direct` and each instance should bind using a
  single routing key (partition).
- The upstream stage must publish batches using a deterministic key hash.

### EOF semantics

- Each upstream EOF increments `ReceivedEOFs`.
- Control EOF messages broadcast the increment to all replicas.
- A flush happens only when `ReceivedEOFs == ExpectedEOFs` and `PendingMessages == 0`.
- Control messages include a per-sender sequence to ignore duplicates.

### Result output

Results are sent as JSON `ResultBatch` with `type=result` and a final
`type=eof` per client/task.
