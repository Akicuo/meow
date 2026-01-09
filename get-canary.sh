#!/usr/bin/env bash

# Query the canary endpoint. Make sure the server is running:
#   go run cmd/canary/main.go
#
# Actual run:
#   $ curl -s http://localhost:9000/canary
#   OK
curl -s http://localhost:9000/canary
