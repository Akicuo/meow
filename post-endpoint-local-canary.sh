#!/usr/bin/env bash

# Create or update the local-canary endpoint on the config server.
curl -i -X POST http://localhost:8000/endpoints/local-canary \
  -H "Content-Type: application/json" \
  --data @new-endpoint.json
