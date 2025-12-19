#!/bin/bash
# Convenience wrapper for integration tests

cd tests/integration
./podman-flow.sh "$@"
