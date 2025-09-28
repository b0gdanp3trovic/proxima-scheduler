import time
import requests

def measure_latency(url: str, delay: float, iterations: int, output_file: str):
    """
    Send requests to the provided URL with a defined delay, measure latency,
    and write results to a file.
    """
    with open(output_file, "w") as f:
        for i in range(iterations):
            start = time.time()
            try:
                response = requests.get(url)
                latency = (time.time() - start) * 1000
                line = f"Request {i+1}: {response.status_code}, latency = {latency:.2f} ms\n"
                print(line.strip())
                f.write(line)
            except requests.exceptions.RequestException as e:
                line = f"Request {i+1} failed: {e}\n"
                print(line.strip())
                f.write(line)

            time.sleep(delay)


if __name__ == "__main__":
    target_url = "http://localhost:8080/test-flask-service"
    delay_seconds = 1
    num_requests = 5000
    output_file = "latencies.txt"

    measure_latency(target_url, delay_seconds, num_requests, output_file)
    print(f"Results written to {output_file}")
