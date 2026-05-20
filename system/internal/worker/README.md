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
- `OutputQueue`
- `ControlExchange`
- `ControlKey`
- `UpstreamInstances` (expected EOFs per client/task)

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
