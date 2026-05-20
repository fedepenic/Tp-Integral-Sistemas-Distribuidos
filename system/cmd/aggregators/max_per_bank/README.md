# MaxPerBank Aggregator

This binary runs the MaxPerBank aggregator over incoming transaction batches.

## Env vars

- `RABBITMQ_HOST`
- `RABBITMQ_PORT`
- `INPUT_EXCHANGE`
- `INPUT_KEY`
- `OUTPUT_QUEUE`
- `EOF_CONTROL_EXCHANGE`
- `EOF_CONTROL_KEY`
- `INSTANCE_ID`
- `UPSTREAM_INSTANCES`
