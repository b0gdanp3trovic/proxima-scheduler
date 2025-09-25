import time
import requests

def measure_latency(url: str, delay: float, iterations: int):
    """
    Send requests to the provided URL with a defined delay and measure latency.

    :param url: Target URL
    :param delay: Delay between requests in seconds
    :param iterations: Number of requests to send
    """
    for i in range(iterations):
        start = time.time()
        try:
            response = requests.get(url)
            latency = (time.time() - start) * 1000
            print(f"Request {i+1}: {response.status_code}, latency = {latency:.2f} ms")
        except requests.exceptions.RequestException as e:
            print(f"Request {i+1} failed: {e}")

        time.sleep(delay)


if __name__ == "__main__":
    target_url = "http://localhost:8080/test-flask-service"
    delay_seconds = 1
    num_requests = 50                  

    measure_latency(target_url, delay_seconds, num_requests)