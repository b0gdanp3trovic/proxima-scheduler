#!/bin/bash

# Fetch the availability zone from Hetzner metadata
ZONE=$(curl -s http://169.254.169.254/hetzner/v1/metadata | grep 'availability-zone' | awk '{print $2}')

# Label the node in Kubernetes
kubectl label node $(hostname) availability-zone=$ZONE --overwrite