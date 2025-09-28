import re
import matplotlib.pyplot as plt

latencies = []
iterations = []

with open("latencies.txt") as f:
    for line in f:
        match = re.search(r"Request (\d+):.*latency = ([\d.]+) ms", line)
        if match:
            iterations.append(int(match.group(1)))
            latencies.append(float(match.group(2)))

plt.figure(figsize=(12, 6))
plt.plot(iterations, latencies, marker="o", linestyle="-")
plt.xlabel("Request number")
plt.ylabel("Latency (ms)")
plt.title("Latency over time")
plt.grid(True)
plt.show()