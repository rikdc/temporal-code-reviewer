#!/bin/sh
# init-temporal.sh — Idempotent Temporal namespace and search-attribute setup.
# Runs as a one-shot container after the Temporal server is healthy.

set -e

TEMPORAL_ADDRESS="${TEMPORAL_ADDRESS:-temporal:7233}"
NAMESPACE="${TEMPORAL_NAMESPACE:-code-reviewer}"

echo "Waiting for Temporal at ${TEMPORAL_ADDRESS}..."
until tctl --address "${TEMPORAL_ADDRESS}" cluster health 2>/dev/null; do
  sleep 2
done
echo "Temporal is healthy."

# Create namespace if it does not exist.
if ! tctl --address "${TEMPORAL_ADDRESS}" namespace describe "${NAMESPACE}" >/dev/null 2>&1; then
  echo "Creating namespace '${NAMESPACE}'..."
  tctl --address "${TEMPORAL_ADDRESS}" namespace register "${NAMESPACE}" --rd 72h
  echo "Namespace '${NAMESPACE}' created."
else
  echo "Namespace '${NAMESPACE}' already exists."
fi

# Register custom search attributes (idempotent — tctl errors if already exist).
echo "Registering search attributes..."
tctl --address "${TEMPORAL_ADDRESS}" --namespace "${NAMESPACE}" \
  search-attribute create Repository TEXT 2>/dev/null || true
tctl --address "${TEMPORAL_ADDRESS}" --namespace "${NAMESPACE}" \
  search-attribute create PRAuthor TEXT 2>/dev/null || true
echo "Search attributes registered."

echo "Temporal initialization complete."
