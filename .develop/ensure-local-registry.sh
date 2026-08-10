#!/usr/bin/env bash

# Ensure the registry used by custom local-registry builds exists before DevSpace starts building.
# DevSpace normally creates this Deployment while initializing its built-in localregistry engine,
# but custom image builders run before that initialization.

set -euo pipefail

namespace=${1:?namespace required}
registry_name=${2:-registry}
kubectl=${KUBECTL:-kubectl}

script_dir=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)
lock_file="${script_dir}/.registry-deploy.lock"

exec 9>"${lock_file}"
flock -x 9

${kubectl} apply -n "${namespace}" -f - <<EOF
apiVersion: apps/v1
kind: Deployment
metadata:
  name: ${registry_name}
  labels:
    app.kubernetes.io/name: clabernetes-devspace-registry
    app.kubernetes.io/component: registry
spec:
  replicas: 1
  selector:
    matchLabels:
      app.kubernetes.io/name: clabernetes-devspace-registry
  template:
    metadata:
      labels:
        app.kubernetes.io/name: clabernetes-devspace-registry
        app.kubernetes.io/component: registry
    spec:
      containers:
        - name: registry
          image: registry:2.8.1
          ports:
            - name: registry
              containerPort: 5000
          readinessProbe:
            httpGet:
              path: /v2/
              port: registry
            initialDelaySeconds: 1
            periodSeconds: 2
---
apiVersion: v1
kind: Service
metadata:
  name: ${registry_name}
  labels:
    app.kubernetes.io/name: clabernetes-devspace-registry
    app.kubernetes.io/component: registry
spec:
  type: NodePort
  selector:
    app.kubernetes.io/name: clabernetes-devspace-registry
  ports:
    - name: registry
      port: 5000
      targetPort: registry
EOF

${kubectl} -n "${namespace}" rollout status "deployment/${registry_name}" --timeout=120s
