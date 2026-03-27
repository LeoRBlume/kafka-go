#!/usr/bin/env bash
set -euo pipefail

CLUSTER_NAME="kafka-go"
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
ROOT_DIR="$(cd "$SCRIPT_DIR/.." && pwd)"

echo "==> Checking kind cluster..."
if ! kind get clusters 2>/dev/null | grep -q "^${CLUSTER_NAME}$"; then
  echo "==> Creating kind cluster '${CLUSTER_NAME}'..."
  kind create cluster --name "$CLUSTER_NAME" --config "$ROOT_DIR/k8s/kind-config.yaml"
else
  echo "==> Cluster '${CLUSTER_NAME}' already exists, skipping creation."
fi

echo "==> Building Docker images..."
docker build -t producer:latest "$ROOT_DIR/producer"
docker build -t consumer:latest "$ROOT_DIR/consumer"

echo "==> Loading images into kind..."
kind load docker-image producer:latest --name "$CLUSTER_NAME"
kind load docker-image consumer:latest --name "$CLUSTER_NAME"

echo "==> Applying Kubernetes manifests..."
kubectl apply -k "$ROOT_DIR/k8s/base"

echo "==> Waiting for Kafka to be ready..."
kubectl rollout status statefulset/kafka -n kafka --timeout=180s

echo "==> Creating Kafka topic..."
kubectl exec -n kafka kafka-0 -- kafka-topics \
  --bootstrap-server localhost:9092 \
  --create --topic demo-topic \
  --partitions 50 --replication-factor 1 \
  --if-not-exists

echo "==> Restarting consumers to pick up topic assignment..."
kubectl rollout restart deployment/consumer -n apps
kubectl rollout status deployment/consumer -n apps --timeout=60s

echo "==> Waiting for apps to be ready..."
kubectl rollout status deployment/producer -n apps --timeout=60s
kubectl rollout status deployment/consumer -n apps --timeout=60s

echo ""
echo "==> Done! Endpoints:"
echo "    Producer: http://localhost:8081"
echo "    Consumer health: http://localhost:8082/health"
echo ""
echo "==> Send messages:"
echo "    curl -s -X POST http://localhost:8081/produce -H 'Content-Type: application/json' -d '{\"count\": 100}'"
