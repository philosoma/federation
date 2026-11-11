#!/usr/bin/env bash
# Phase 13: SPIRE trust-domain federation. Deferred on our side.
. "$FED_SCRIPTS/lib.sh"
echo "NOT DONE by design: our SPIRE server runs as a systemd service and goat-peering-spire"
echo "expects a container, so peering_spire_enabled is false for every peering."
show spire "NOT DONE by design"
