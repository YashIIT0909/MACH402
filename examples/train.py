"""A toy 'training' job: fits a line by gradient descent and saves the weights.

Runs in a few seconds on CPU, prints progress so log streaming is visible, and
leaves an artifact in /out so the download path is exercised too.
"""
import json, pathlib, random

random.seed(7)
# y = 3x + 2 with a little noise
data = [(x, 3 * x + 2 + random.uniform(-0.1, 0.1)) for x in range(50)]

w, b, lr = 0.0, 0.0, 0.001
for epoch in range(400):
    dw = db = 0.0
    for x, y in data:
        error = (w * x + b) - y
        dw += 2 * error * x
        db += 2 * error
    w -= lr * dw / len(data)
    b -= lr * db / len(data)
    if epoch % 100 == 0:
        print(f"epoch {epoch:3d}  w={w:.4f}  b={b:.4f}")

print(f"done      w={w:.4f}  b={b:.4f}  (true w=3, b=2)")
pathlib.Path("/out/weights.json").write_text(json.dumps({"w": w, "b": b}, indent=2))
print("wrote /out/weights.json")
