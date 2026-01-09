#!/usr/bin/env bash

# Fetch all configured endpoints from the config server (localhost:8000)
# and store the response.
curl -s http://localhost:8000/endpoints > all-endpoints.json
