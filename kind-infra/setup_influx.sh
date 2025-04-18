docker network create kindinfra

# Connect clusters + edge nodes to the network
docker network connect kindinfra proxima1-control-plane
docker network connect kindinfra proxima1-worker

docker run -d \
  --name influxdb \
  --network kindinfra \
  -p 8086:8086 \
  -v influxdb-data:/var/lib/influxdb2 \
  -e DOCKER_INFLUXDB_INIT_MODE=setup \
  -e DOCKER_INFLUXDB_INIT_USERNAME=admin \
  -e DOCKER_INFLUXDB_INIT_PASSWORD=supersecretpassword \
  -e DOCKER_INFLUXDB_INIT_ORG=proxima \
  -e DOCKER_INFLUXDB_INIT_BUCKET=proxima \
  -e DOCKER_INFLUXDB_INIT_ADMIN_TOKEN=supersecrettoken \
  influxdb:2.7.11