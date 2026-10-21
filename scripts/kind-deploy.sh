#!/usr/bin/env bash
set -euo pipefail

CLUSTER=${CLUSTER:-linkpulse}
TAG=${TAG:-dev}
REGISTRY=${REGISTRY:-ghcr.io/piliotov}
BUILD=${BUILD:-true}

if ! kind get clusters | grep -qx "$CLUSTER"; then
  kind create cluster --name "$CLUSTER" --wait 120s
fi

for svc in shortener analytics; do
  image="$REGISTRY/linkpulse-$svc:$TAG"
  if [ "$BUILD" = true ]; then
    docker build -t "$image" "services/$svc"
  fi
  kind load docker-image "$image" --name "$CLUSTER"
done

helm upgrade --install linkpulse deploy/helm/linkpulse \
  --set imageRegistry="$REGISTRY" \
  --set shortener.image.tag="$TAG" \
  --set analytics.image.tag="$TAG" \
  --set ingress.enabled=false \
  --wait --timeout 3m

kubectl get pods -l app.kubernetes.io/instance=linkpulse
