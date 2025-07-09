import numpy as np
import matplotlib.pyplot as plt

k = 0.02
L_min = 20

latencies = np.linspace(0, 500, 500)

scores = 1 / (1 + np.exp(k * (latencies - L_min)))

example_latencies = [20, 100, 200, 300, 400]
example_scores = 1 / (1 + np.exp(k * (np.array(example_latencies) - L_min)))

plt.figure(figsize=(10,6))
plt.plot(latencies, scores, label="Sigmoid Score Curve", color="blue")
plt.scatter(example_latencies, example_scores, color="red", zorder=5, label="Example Latencies")

for x, y in zip(example_latencies, example_scores):
    plt.annotate(
        f"{x} ms\n{y:.2f}",
        xy=(x, y),
        xytext=(x+10, y+0.05),
        arrowprops=dict(arrowstyle="->", lw=0.8),
        fontsize=9
    )

plt.xlabel("Latency (ms)")
plt.ylabel("Score")
plt.title("Sigmoid Transformation of Latency")
plt.grid(True)
plt.legend()
plt.tight_layout()
plt.show()
