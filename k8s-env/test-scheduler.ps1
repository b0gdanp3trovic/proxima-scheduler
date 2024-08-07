$podName = "test-pod-nginx"

Write-Host "Deleting existing pod $podName (if any)..."
kubectl delete pod $podName --ignore-not-found

Write-Host "Waiting for pod $podName to be fully deleted..."
while (kubectl get pod $podName -ErrorAction SilentlyContinue) {
    Start-Sleep -Seconds 1
}

Write-Host "Applying pod manifest..."
kubectl apply -f deployment-nginx.yaml

Write-Host "Waiting for pod $podName to be scheduled..."
kubectl wait --for=condition=Scheduled pod/$podName --timeout=60s

Write-Host "Pod details:"
kubectl get pod $podName -o wide

Write-Host "Done!"
