import requests
import time
import threading
import signal
import sys

REQUESTS_PER_MINUTE = 60
CONCURRENCY = 10

interval = 60 / REQUESTS_PER_MINUTE
stop_event = threading.Event()

def send_request(i, url):
    try:
        start = time.time()
        response = requests.get(url)
        latency = time.time() - start
        print(f"[{i}] Status: {response.status_code}, Time: {latency:.3f}s")
    except Exception as e:
        print(f"[{i}] Request failed: {e}")

def main():
    if len(sys.argv) != 2:
        print(f"Usage: python {sys.argv[0]} <URL>")
        sys.exit(1)

    url = sys.argv[1]
    i = 0
    threads = []

    def signal_handler(sig, frame):
        print("Stopping...")
        stop_event.set()

    signal.signal(signal.SIGINT, signal_handler)

    while not stop_event.is_set():
        while threading.active_count() - 1 >= CONCURRENCY:
            time.sleep(0.01)

        t = threading.Thread(target=send_request, args=(i, url))
        t.start()
        threads.append(t)
        i += 1

        time.sleep(interval)

    for t in threads:
        t.join()

if __name__ == "__main__":
    main()
