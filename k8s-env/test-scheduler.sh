#!/bin/bash

POD_NAME="test-pod-nginx"

echo "Deleting existing pod $POD_NAME (if any)..."
kubectl delete pod $POD_NAME --ignore-not-found=true

echo "Waiting for pod $POD_NAME to be fully deleted..."
while kubectl get pod $POD_NAME &> /dev/null; do sleep 1; done

echo "Applying pod manifest..."
kubectl apply -f test-pod.yaml

echo "Waiting for pod $POD_NAME to be scheduled..."
kubectl wait --for=condition=Scheduled pod/$POD_NAME --timeout=60s

echo "Pod details:"
kubectl get pod $POD_NAME -o wide

echo "Done!"
