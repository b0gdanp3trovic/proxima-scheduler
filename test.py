import requests
import time
import threading

URL = "http://localhost:8080/test-flask-service"
TOTAL_REQUESTS = 100
CONCURRENCY = 10

def send_request(i):
    try:
        start = time.time()
        response = requests.get(URL)
        latency = time.time() - start
        print(f"[{i}] Status: {response.status_code}, Time: {latency:.3f}s")
    except Exception as e:
        print(f"[{i}] Request failed: {e}")

def main():
    threads = []
    for i in range(TOTAL_REQUESTS):
        t = threading.Thread(target=send_request, args=(i,))
        threads.append(t)
        t.start()

        if i % CONCURRENCY == 0:
            time.sleep(1)

    for t in threads:
        t.join()

if __name__ == "__main__":
    main()
