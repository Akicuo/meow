#!/usr/bin/env bash

# Fetch a single endpoint (local-canary) from the config server.
curl -s http://localhost:8000/endpoints/local-canary > endpoint-local-canary.json
